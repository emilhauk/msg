package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/emilhauk/msg/internal/handler"
	"github.com/emilhauk/msg/internal/middleware"
	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/storage"
	"github.com/emilhauk/msg/internal/testutil"
)

func newTranscodeHandler(t *testing.T) (*handler.TranscodeHandler, *[]byte) {
	t.Helper()
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)

	var stored []byte
	s3srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stored, _ = io.ReadAll(r.Body)
	}))
	t.Cleanup(s3srv.Close)
	s3c, err := storage.NewS3Client(storage.Config{Endpoint: s3srv.URL, Bucket: "media", Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "b"})
	require.NoError(t, err)

	script := filepath.Join(t.TempDir(), "ffmpeg")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nfor a; do last=$a; done\ncp \"$3\" \"$last\"\n"), 0o755))
	prev := handler.FFmpegPath
	handler.FFmpegPath = script
	t.Cleanup(func() { handler.FFmpegPath = prev })

	return &handler.TranscodeHandler{Redis: ts.Redis, S3: s3c}, &stored
}

func doTranscode(h *handler.TranscodeHandler, contentType string, body io.Reader, contentLength int64) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/rooms/"+testRoom+"/transcode", body)
	req.ContentLength = contentLength
	req.Header.Set("Content-Type", contentType)
	req.SetPathValue("id", testRoom)
	u := alice
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, &u))
	rec := httptest.NewRecorder()
	h.HandleTranscode(rec, req)
	return rec
}

func TestTranscode_Success(t *testing.T) {
	h, stored := newTranscodeHandler(t)
	rec := doTranscode(h, "video/quicktime", strings.NewReader("fake mov bytes"), 14)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var att model.Attachment
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &att))
	require.Equal(t, "video/mp4", att.ContentType)
	require.Contains(t, att.URL, "/media/rooms/"+testRoom+"/")
	require.True(t, strings.HasSuffix(att.URL, att.Filename+".mp4"))
	require.Contains(t, string(*stored), "fake mov bytes")
}

func TestTranscode_HeartbeatsWhileEncoding(t *testing.T) {
	h, _ := newTranscodeHandler(t)
	require.NoError(t, os.WriteFile(handler.FFmpegPath, []byte("#!/bin/sh\nsleep 0.2\nfor a; do last=$a; done\ncp \"$3\" \"$last\"\n"), 0o755))
	prev := handler.HeartbeatInterval
	handler.HeartbeatInterval = 20 * time.Millisecond
	t.Cleanup(func() { handler.HeartbeatInterval = prev })

	rec := doTranscode(h, "video/quicktime", strings.NewReader("fake mov bytes"), 14)
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, rec.Flushed)
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	require.Equal(t, 1, len(lines), "heartbeats must be bare newlines")
	require.True(t, strings.HasPrefix(rec.Body.String(), "\n\n"), rec.Body.String())
	var att model.Attachment
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &att))
	require.Equal(t, "video/mp4", att.ContentType)
}

func TestTranscode_FFmpegFailureReportedInBody(t *testing.T) {
	h, _ := newTranscodeHandler(t)
	require.NoError(t, os.WriteFile(handler.FFmpegPath, []byte("#!/bin/sh\nexit 1\n"), 0o755))

	rec := doTranscode(h, "video/quicktime", strings.NewReader("fake mov bytes"), 14)
	require.Equal(t, http.StatusOK, rec.Code)
	var res struct{ Error string }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	require.Equal(t, "could not transcode video", res.Error)
}

func TestTranscode_Rejects(t *testing.T) {
	h, _ := newTranscodeHandler(t)
	require.Equal(t, http.StatusBadRequest, doTranscode(h, "image/png", strings.NewReader("x"), 1).Code)
	require.Equal(t, http.StatusRequestEntityTooLarge, doTranscode(h, "video/quicktime", strings.NewReader("x"), 100<<20+1).Code)
	require.Equal(t, http.StatusRequestEntityTooLarge, doTranscode(h, "video/quicktime", io.LimitReader(zeroReader{}, 100<<20+1), -1).Code)
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
