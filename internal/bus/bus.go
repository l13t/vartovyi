package bus

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/trace"
	"strings"
	"time"
	"vartovyi/internal/model"
	"vartovyi/internal/store"
	"vartovyi/internal/telemetry"
)

type Bus struct {
	Conn *nats.Conn
	JS   nats.JetStreamContext
}

func Open(url string) (*Bus, error) {
	nc, e := nats.Connect(url, nats.Timeout(5*time.Second), nats.MaxReconnects(-1), nats.ReconnectWait(time.Second))
	if e != nil {
		return nil, e
	}
	js, e := nc.JetStream(nats.MaxWait(5 * time.Second))
	if e != nil {
		nc.Close()
		return nil, e
	}
	cfg := &nats.StreamConfig{Name: "VARTOVYI", Subjects: []string{"vartovyi.v1.>"}, Storage: nats.FileStorage, Retention: nats.LimitsPolicy, MaxAge: 7 * 24 * time.Hour, MaxBytes: 256 << 20, Discard: nats.DiscardNew, Replicas: 1}
	if _, e = js.StreamInfo(cfg.Name); e != nil {
		if _, e = js.AddStream(cfg); e != nil {
			nc.Close()
			return nil, e
		}
	}
	return &Bus{nc, js}, nil
}
func (b *Bus) Close() { b.Conn.Close() }
func (b *Bus) Outbox(ctx context.Context, s *store.Store) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	rows, e := tx.Query(ctx, "SELECT id,subject,payload,trace_context FROM event_outbox ORDER BY created_at LIMIT 32 FOR UPDATE SKIP LOCKED")
	if e != nil {
		return e
	}
	type item struct {
		id, subject string
		payload     []byte
		carrier     map[string]string
	}
	items := []item{}
	for rows.Next() {
		var v item
		if e = rows.Scan(&v.id, &v.subject, &v.payload, &v.carrier); e != nil {
			rows.Close()
			return e
		}
		items = append(items, v)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, v := range items {
		msg := nats.NewMsg(v.subject)
		msg.Header.Set(nats.MsgIdHdr, v.id)
		msg.Data = v.payload
		if e = b.publish(telemetry.Extract(ctx, v.carrier), msg); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "DELETE FROM event_outbox WHERE id=$1", v.id); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (b *Bus) Subscribe() (*nats.Subscription, error) {
	return b.JS.PullSubscribe("vartovyi.v1.motion", "telegram", nats.BindStream("VARTOVYI"), nats.ManualAck(), nats.AckExplicit(), nats.MaxDeliver(10), nats.AckWait(time.Minute))
}
func Decode(msg *nats.Msg) (model.Event, error) {
	var v model.Event
	e := json.Unmarshal(msg.Data, &v)
	if e == nil && (v.Version != 1 || v.Type != "motion" || !model.ValidID(v.ID) || !model.ValidID(v.CameraID)) {
		e = errors.New("invalid event")
	}
	return v, e
}

func (b *Bus) publish(ctx context.Context, msg *nats.Msg) (err error) {
	ctx, span := telemetry.Start(ctx, "vartovyi.v1.motion publish", trace.SpanKindProducer)
	defer func() { telemetry.End(span, err) }()
	// Unlike http.Header, NATS headers are case-sensitive. Keep W3C keys lowercase.
	for key, value := range telemetry.Carrier(ctx) {
		msg.Header.Set(key, value)
	}
	_, err = b.JS.PublishMsg(msg, nats.Context(ctx))
	return err
}

// MessageContext resumes only W3C trace metadata from a JetStream message.
func MessageContext(ctx context.Context, msg *nats.Msg) context.Context {
	carrier := map[string]string{}
	for key := range msg.Header {
		if strings.EqualFold(key, "traceparent") || strings.EqualFold(key, "tracestate") {
			carrier[strings.ToLower(key)] = msg.Header.Get(key)
		}
	}
	return telemetry.Extract(ctx, carrier)
}
