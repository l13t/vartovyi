package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"time"
	"vartovyi/internal/config"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(1)
	}
	kind := os.Args[1]
	if kind != "create" && kind != "complete" {
		os.Exit(1)
	}
	c, e := config.Load()
	if e != nil {
		os.Exit(1)
	}
	duration, _ := strconv.ParseFloat(os.Getenv("MTX_SEGMENT_DURATION"), 64)
	b, _ := json.Marshal(map[string]any{"path": os.Getenv("MTX_SEGMENT_PATH"), "duration": duration})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 3 * time.Second}
	for ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, "POST", c.RecorderURL+"/internal/hooks/"+kind, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+c.ServiceToken)
		req.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(req)
		if e == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return
			}
		}
		select {
		case <-ctx.Done():
			os.Exit(1)
		case <-time.After(time.Second):
		}
	}
	os.Exit(1)
}
