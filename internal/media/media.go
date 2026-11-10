package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vartovyi/internal/model"
	"vartovyi/internal/telemetry"
)

// AtomicJSON persists manifests before metadata is acknowledged to the sender.
func AtomicJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".pending-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return e
	}
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}

// Private manifests include paths that are deliberately excluded from public
// Segment JSON. Do not reuse the public wire encoding as the durable format.
type segmentManifest struct {
	model.Segment
	RelativePath string `json:"relative_path"`
}

func WriteSegmentManifest(archive string, s model.Segment) error {
	if !filepath.IsLocal(s.Path) {
		return errors.New("manifest path must be relative")
	}
	return AtomicJSON(filepath.Join(archive, s.Path)+".json", segmentManifest{s, s.Path})
}
func ReadSegmentManifest(path string) (model.Segment, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return model.Segment{}, e
	}
	var v segmentManifest
	if e = json.Unmarshal(b, &v); e != nil {
		return model.Segment{}, e
	}
	if !filepath.IsLocal(v.RelativePath) {
		return model.Segment{}, errors.New("invalid manifest path")
	}
	v.Segment.Path = v.RelativePath
	return v.Segment, nil
}
func Lock(dir string) (*os.File, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(filepath.Join(dir, ".recorder.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("archive already owned by another recorder")
	}
	return f, nil
}
func Run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 3 * time.Second
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	return cmd.Run()
}
func IngestArgs(c model.Camera, dest string) []string {
	if c.Source == "rtsp" && c.Transcode {
		return []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-fflags", "+genpts+nobuffer", "-analyzeduration", "100000", "-probesize", "32768", "-use_wallclock_as_timestamps", "1", "-rtsp_transport", c.Transport(), "-timeout", "10000000", "-threads", "2", "-i", c.URL, "-map", "0:v:0", "-an", "-vf", "setpts=PTS-STARTPTS,scale=trunc(iw/2)*2:trunc(ih/2)*2,format=yuv420p", "-fps_mode", "vfr", "-c:v", "libx264", "-threads", "2", "-preset", "ultrafast", "-tune", "zerolatency", "-profile:v", "baseline", "-crf", "23", "-g", strconv.Itoa(c.FPS * 2), "-force_key_frames", "expr:gte(t,n_forced*2)", "-bf", "0", "-f", "rtsp", "-rtsp_transport", "tcp", dest}
	}
	return []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-fflags", "+genpts", "-r", strconv.Itoa(c.FPS), "-f", "h264", "-rw_timeout", "10000000", "-i", c.URL, "-map", "0:v:0", "-an", "-c:v", "copy", "-f", "rtsp", "-rtsp_transport", "tcp", dest}
}
func Probe(ctx context.Context, path string) (float64, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	b, e := cmd.Output()
	if e != nil {
		return 0, errors.New("segment probe failed")
	}
	v, e := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if e != nil || v <= 0 {
		return 0, errors.New("segment duration unavailable")
	}
	return v, nil
}
func Snapshot(ctx context.Context, source, path string) error {
	return Run(ctx, "-nostdin", "-loglevel", "error", "-rtsp_transport", "tcp", "-timeout", "10000000", "-threads", "2", "-i", source, "-frames:v", "1", "-vf", "scale=640:-2", "-y", path)
}
func Export(ctx context.Context, segments []model.Segment, start, end time.Time, dest string) error {
	if len(segments) == 0 {
		return errors.New("no recordings for interval")
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].Start.Before(segments[j].Start) })
	// Only contiguous intervals can form one MP4; do not silently conceal gaps.
	for i := 1; i < len(segments); i++ {
		if segments[i].Start.Sub(segments[i-1].End) > 2*time.Second {
			return errors.New("interval contains a recording gap; export each span separately")
		}
	}
	f, e := os.CreateTemp(filepath.Dir(dest), "concat-*.txt")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	for _, s := range segments {
		if strings.ContainsAny(s.Path, "'\r\n") {
			f.Close()
			return errors.New("invalid segment path")
		}
		fmt.Fprintf(f, "file '%s'\n", s.Path)
	}
	if e = f.Close(); e != nil {
		return e
	}
	if segments[0].Start.After(start.Add(2*time.Second)) || segments[len(segments)-1].End.Before(end.Add(-2*time.Second)) {
		return errors.New("requested interval is not fully recorded")
	}
	duration := end.Sub(segments[0].Start).Seconds()
	if duration <= 0 {
		return errors.New("invalid interval")
	}
	return Run(ctx, "-nostdin", "-loglevel", "error", "-f", "concat", "-safe", "0", "-i", f.Name(), "-t", fmt.Sprint(duration), "-c", "copy", "-movflags", "+faststart", "-y", dest)
}
func Stream(ctx context.Context, source string) (io.ReadCloser, func(), error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-loglevel", "error", "-rtsp_transport", "tcp", "-timeout", "10000000", "-threads", "2", "-i", source, "-an", "-vf", "fps=2,scale=320:180", "-pix_fmt", "gray", "-threads", "1", "-f", "rawvideo", "pipe:1")
	pipe, e := cmd.StdoutPipe()
	if e != nil {
		return nil, nil, e
	}
	if e = cmd.Start(); e != nil {
		return nil, nil, e
	}
	done := func() {
		pipe.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}
	return pipe, done, nil
}

type MediaMTX struct {
	Base, Token string
	Client      *http.Client
}

func (m MediaMTX) Set(ctx context.Context, c model.Camera) error {
	source := "publisher"
	if c.Source == "rtsp" && !c.Transcode {
		source = c.URL
	}
	cfg := map[string]any{"source": source, "rtspTransport": c.Transport(), "record": true, "recordFormat": "fmp4", "recordSegmentDuration": "10s", "recordPartDuration": "1s", "recordDeleteAfter": "0s", "recordPath": "/archive/segments/%path/%Y-%m-%dT%H-%M-%S.%f%z", "runOnRecordSegmentCreate": "/usr/local/bin/hook create", "runOnRecordSegmentComplete": "/usr/local/bin/hook complete", "overridePublisher": false}
	return m.request(ctx, "POST", "/v3/config/paths/add/"+c.ID, cfg)
}
func (m MediaMTX) Delete(ctx context.Context, id string) error {
	return m.request(ctx, "DELETE", "/v3/config/paths/delete/"+id, nil)
}
func (m MediaMTX) Substream(ctx context.Context, c model.Camera) error {
	if c.Substream == "" {
		return nil
	}
	return m.request(ctx, "POST", "/v3/config/paths/add/"+c.ID+"-sub", map[string]any{"source": c.Substream, "record": false, "rtspTransport": c.Transport()})
}
func (m MediaMTX) request(ctx context.Context, method, path string, v any) error {
	var body io.Reader
	if v != nil {
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		body = strings.NewReader(string(b))
	}
	req, e := http.NewRequestWithContext(ctx, method, m.Base+path, body)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("service", m.Token)
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second, Transport: telemetry.Transport{}}
	}
	resp, e := client.Do(req)
	if e != nil {
		return errors.New("media control unavailable")
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("media control returned %d", resp.StatusCode)
	}
	return nil
}

func (m MediaMTX) Exists(ctx context.Context, id string) bool {
	return m.request(ctx, "GET", "/v3/config/paths/get/"+id, nil) == nil
}

func DiskFree(path string) (uint64, error) {
	var v syscall.Statfs_t
	if e := syscall.Statfs(path, &v); e != nil {
		return 0, e
	}
	return v.Bavail * uint64(v.Bsize), nil
}
func (m MediaMTX) Reset(ctx context.Context) error {
	req, e := http.NewRequestWithContext(ctx, "GET", m.Base+"/v3/config/paths/list?itemsPerPage=1000", nil)
	if e != nil {
		return e
	}
	req.SetBasicAuth("service", m.Token)
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second, Transport: telemetry.Transport{}}
	}
	resp, e := client.Do(req)
	if e != nil {
		return errors.New("media unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("media unavailable")
	}
	var result struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); e != nil {
		return e
	}
	for _, p := range result.Items {
		if model.ValidID(p.Name) {
			if e = m.Delete(ctx, p.Name); e != nil {
				return e
			}
		}
	}
	return nil
}
