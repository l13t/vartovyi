package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/trace"
	"vartovyi/internal/bus"
	"vartovyi/internal/config"
	"vartovyi/internal/model"
	run "vartovyi/internal/runtime"
	"vartovyi/internal/store"
	"vartovyi/internal/telemetry"
)

type Notifier struct {
	Config config.Config
	Store  *store.Store
	client *http.Client
}

func New(c config.Config, s *store.Store) *Notifier {
	return &Notifier{c, s, &http.Client{Timeout: 20 * time.Second}}
}
func (n *Notifier) Run(ctx context.Context) {
	for ctx.Err() == nil {
		b, e := bus.Open(n.Config.NATSURL)
		if e == nil {
			n.consume(ctx, b)
			b.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}
func (n *Notifier) consume(ctx context.Context, b *bus.Bus) {
	sub, e := b.Subscribe()
	if e != nil {
		return
	}
	defer sub.Unsubscribe()
	for ctx.Err() == nil {
		messages, e := sub.Fetch(1, nats.MaxWait(2*time.Second))
		if errors.Is(e, nats.ErrTimeout) {
			continue
		}
		if e != nil {
			return
		}
		for _, msg := range messages {
			_ = n.process(ctx, msg)
		}
	}
}
func (n *Notifier) process(ctx context.Context, msg *nats.Msg) (err error) {
	ctx, span := telemetry.Start(bus.MessageContext(ctx, msg), "vartovyi.v1.motion process", trace.SpanKindConsumer)
	defer func() { telemetry.End(span, err) }()
	event, err := bus.Decode(msg)
	if err != nil {
		_ = msg.Term()
		return err
	}
	if !event.Notify {
		return msg.Ack()
	}
	settings, err := n.Store.Telegram(ctx, n.Config.TelegramToken, n.Config.TelegramChat)
	if err != nil {
		_ = msg.NakWithDelay(30 * time.Second)
		return err
	}
	if !settings.Enabled {
		return msg.Ack()
	}
	c, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err = n.deliver(c, event, settings); err != nil {
		_ = msg.NakWithDelay(30 * time.Second)
		return err
	}
	return msg.Ack()
}

func (n *Notifier) deliver(ctx context.Context, event model.Event, settings store.TelegramSettings) error {
	tag, e := n.Store.Pool.Exec(ctx, "INSERT INTO notifier_deliveries(id,state,attempts) VALUES($1,'sending',1) ON CONFLICT(id) DO UPDATE SET state='sending',attempts=notifier_deliveries.attempts+1,updated_at=now() WHERE notifier_deliveries.state='retry' OR (notifier_deliveries.state='sending' AND notifier_deliveries.updated_at<now()-interval '1 minute')", event.ID)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		var state string
		e = n.Store.Pool.QueryRow(ctx, "SELECT state FROM notifier_deliveries WHERE id=$1", event.ID).Scan(&state)
		if e != nil {
			return e
		}
		if state == "sent" || state == "skipped" {
			return nil
		}
		return errors.New("delivery in progress")
	}
	var recent int
	e = n.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM notifier_deliveries d JOIN recorder_events e ON e.id=d.id WHERE e.camera_id=$1 AND d.state='sent' AND d.updated_at>now()-interval '30 seconds'", event.CameraID).Scan(&recent)
	if e != nil {
		return e
	}
	state := "sent"
	if recent > 0 {
		state = "skipped"
	} else {
		e = n.send(ctx, event, settings)
		if e != nil {
			state = "retry"
		}
	}
	_, dbErr := n.Store.Pool.Exec(ctx, "UPDATE notifier_deliveries SET state=$2,updated_at=now() WHERE id=$1", event.ID, state)
	if dbErr != nil {
		return dbErr
	}
	return e
}
func (n *Notifier) send(ctx context.Context, event model.Event, settings store.TelegramSettings) error {
	caption := fmt.Sprintf("Рух · %s\n%s\n%s/cameras/%s?at=%s", event.CameraID, event.At.UTC().Format(time.RFC3339), strings.TrimSuffix(n.Config.PublicURL, "/"), event.CameraID, event.At.UTC().Format(time.RFC3339))
	var photo []byte
	if shard := n.Config.ShardURLs[event.ShardID]; shard != "" {
		req, _ := http.NewRequestWithContext(ctx, "GET", shard+"/files/snapshots/"+event.ID, nil)
		req.Header.Set("Authorization", "Bearer "+n.Config.ServiceToken)
		client := *n.client
		client.Transport = telemetry.Transport{Base: n.client.Transport, Propagate: true}
		resp, e := client.Do(req)
		if e == nil {
			if resp.StatusCode == 200 {
				photo, _ = io.ReadAll(io.LimitReader(resp.Body, 5<<20))
			}
			resp.Body.Close()
		}
	}
	method := "sendMessage"
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("chat_id", settings.ChatID)
	if len(photo) > 0 {
		method = "sendPhoto"
		_ = writer.WriteField("caption", caption)
		part, e := writer.CreateFormFile("photo", "event.jpg")
		if e != nil {
			return e
		}
		if _, e = part.Write(photo); e != nil {
			return e
		}
	} else {
		_ = writer.WriteField("text", caption)
	}
	if e := writer.Close(); e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", "https://api.telegram.org/bot"+settings.Token+"/"+method, &body)
	if e != nil {
		return errors.New("invalid Telegram configuration")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	client := *n.client
	client.Transport = telemetry.Transport{Base: n.client.Transport}
	resp, e := client.Do(req)
	if e != nil {
		return errors.New("Telegram request failed")
	}
	defer resp.Body.Close()
	var result struct {
		OK bool `json:"ok"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&result); e != nil || resp.StatusCode != 200 || !result.OK {
		return errors.New("Telegram delivery rejected")
	}
	return nil
}
func (n *Notifier) Handler() http.Handler {
	m := http.NewServeMux()
	run.Health(m, func() error { return n.Store.Ping(context.Background()) })
	return run.Internal(n.Config.ServiceToken, m)
}
