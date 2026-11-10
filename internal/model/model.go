package model

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"net/url"
	"regexp"
	"time"
)

type Role string

const (
	Admin   Role = "admin"
	Manager Role = "manager"
	Viewer  Role = "viewer"
)

func (r Role) Valid() bool     { return r == Admin || r == Manager || r == Viewer }
func (r Role) CanManage() bool { return r == Admin || r == Manager }

var safeID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func ValidID(id string) bool { return safeID.MatchString(id) }
func ID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type Zone struct {
	Name    string  `json:"name"`
	Exclude bool    `json:"exclude"`
	Points  []Point `json:"points"`
}
type Camera struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	ShardID         string  `json:"shard_id"`
	Source          string  `json:"source"`
	RTSPTransport   string  `json:"rtsp_transport,omitempty"`
	Transcode       bool    `json:"transcode"`
	URL             string  `json:"url,omitempty"`
	Substream       string  `json:"substream,omitempty"`
	FPS             int     `json:"fps"`
	Enabled         bool    `json:"enabled"`
	Mode            string  `json:"mode"`
	RetentionDays   int     `json:"retention_days"`
	MaxSegments     int64   `json:"max_segments"`
	GroupID         string  `json:"group_id"`
	QuotaBytes      int64   `json:"quota_bytes"`
	PreSeconds      int     `json:"pre_seconds"`
	PostSeconds     int     `json:"post_seconds"`
	Threshold       float64 `json:"threshold"`
	MotionEnabled   *bool   `json:"motion_enabled,omitempty"`
	MotionFullFrame bool    `json:"motion_full_frame"`
	Zones           []Zone  `json:"zones"`
	Notify          bool    `json:"notify"`
	Revision        int64   `json:"revision"`
}

func (c Camera) Validate() error {
	if !ValidID(c.ID) || !ValidID(c.ShardID) || c.Name == "" || len(c.Name) > 200 {
		return errors.New("invalid camera identity")
	}
	if c.Source != "rtsp" && c.Source != "h264_tcp" {
		return errors.New("source must be rtsp or h264_tcp")
	}
	if c.RTSPTransport != "" && c.RTSPTransport != "tcp" && c.RTSPTransport != "udp" {
		return errors.New("invalid RTSP transport")
	}
	if c.Transcode && c.Source != "rtsp" {
		return errors.New("transcoding requires RTSP")
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Hostname() == "" || (c.Source == "rtsp" && u.Scheme != "rtsp" && u.Scheme != "rtsps") || (c.Source == "h264_tcp" && u.Scheme != "tcp") {
		return errors.New("invalid source URL")
	}
	if c.Source == "h264_tcp" && (u.User != nil || u.Port() == "" || u.RawQuery != "" || (u.Path != "" && u.Path != "/")) {
		return errors.New("TCP source requires host:port without credentials, path, or options")
	}
	if c.Substream != "" {
		s, e := url.Parse(c.Substream)
		if e != nil || s.Hostname() == "" || (s.Scheme != "rtsp" && s.Scheme != "rtsps") {
			return errors.New("substream must be RTSP")
		}
	}
	if c.FPS < 1 || c.FPS > 60 || (c.Mode != "continuous" && c.Mode != "motion") || c.RetentionDays < 0 || c.RetentionDays > 3650 || c.QuotaBytes < 0 || c.QuotaBytes > 1<<50 || c.MaxSegments < 0 || c.MaxSegments > 100000000 || (c.GroupID != "" && !ValidID(c.GroupID)) {
		return errors.New("invalid recording policy")
	}
	if c.PreSeconds < 0 || c.PreSeconds > 120 || c.PostSeconds < 1 || c.PostSeconds > 120 || c.Threshold <= 0 || c.Threshold > 1 || math.IsNaN(c.Threshold) {
		return errors.New("invalid motion policy")
	}
	if len(c.Zones) > 32 {
		return errors.New("too many zones")
	}
	for _, z := range c.Zones {
		if len(z.Points) < 3 || len(z.Points) > 64 {
			return errors.New("zone needs 3 to 64 points")
		}
		for _, p := range z.Points {
			if math.IsNaN(p.X) || math.IsNaN(p.Y) || p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 {
				return errors.New("zone coordinates must be normalized")
			}
		}
	}
	return nil
}

// Missing motion_enabled preserves detection for existing camera records.
func (c Camera) MotionDetectionEnabled() bool { return c.MotionEnabled == nil || *c.MotionEnabled }
func (c Camera) UsesFullFrame() bool          { return c.MotionFullFrame || len(c.Zones) == 0 }
func (c Camera) MotionZones() []Zone {
	if c.MotionFullFrame {
		return nil
	}
	return c.Zones
}
func (c Camera) Transport() string {
	if c.RTSPTransport == "udp" {
		return "udp"
	}
	return "tcp"
}
func (c Camera) NeedsPublisher() bool {
	return c.Source == "h264_tcp" || (c.Source == "rtsp" && c.Transcode)
}
func DefaultCamera() Camera {
	return Camera{ID: ID(), ShardID: "main", Source: "h264_tcp", FPS: 10, Enabled: true, Mode: "continuous", RetentionDays: 14, PreSeconds: 10, PostSeconds: 20, Threshold: 0.03, Zones: []Zone{}}
}
func (c Camera) Public() Camera { c.URL = ""; c.Substream = ""; return c }

type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Name         string `json:"name"`
	Language     string `json:"language"`
	Role         Role   `json:"role"`
	PasswordHash string `json:"-"`
	Subject      string `json:"-"`
}

func (u User) DisplayName() string {
	if u.Name != "" {
		return u.Name
	}
	return u.Username
}

// ProfileChanges deliberately has no account ID or role: a user can only edit
// their own presentation and credentials, regardless of their installation role.
type ProfileChanges struct {
	Name            *string `json:"name,omitempty"`
	Language        *string `json:"language,omitempty"`
	CurrentPassword string  `json:"current_password,omitempty"`
	NewPassword     string  `json:"new_password,omitempty"`
}
type Principal struct {
	User      User
	SessionID string
	CSRF      string
}
type Segment struct {
	ID       string    `json:"id"`
	CameraID string    `json:"camera_id"`
	ShardID  string    `json:"shard_id"`
	Path     string    `json:"-"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Bytes    int64     `json:"bytes"`
	Keep     bool      `json:"keep"`
}
type Event struct {
	ID       string    `json:"id"`
	Version  int       `json:"version"`
	Type     string    `json:"type"`
	CameraID string    `json:"camera_id"`
	ShardID  string    `json:"shard_id"`
	At       time.Time `json:"at"`
	End      time.Time `json:"end"`
	Score    float64   `json:"score"`
	Notify   bool      `json:"notify"`
	Snapshot string    `json:"-"`
}
type Export struct {
	ID       string    `json:"id"`
	UserID   string    `json:"-"`
	CameraID string    `json:"camera_id"`
	ShardID  string    `json:"shard_id"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Status   string    `json:"status"`
	Error    string    `json:"error,omitempty"`
	Path     string    `json:"-"`
}
type Shard struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}
type Status struct {
	CameraID    string    `json:"camera_id"`
	ShardID     string    `json:"shard_id"`
	State       string    `json:"state"`
	LastSegment time.Time `json:"last_segment"`
	DetectorAt  time.Time `json:"detector_at"`
}

type MotionSignal struct {
	CameraID string  `json:"camera_id"`
	Active   bool    `json:"active"`
	Score    float64 `json:"score"`
}
