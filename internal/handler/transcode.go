package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/emilhauk/msg/internal/middleware"
	"github.com/emilhauk/msg/internal/model"
	redisclient "github.com/emilhauk/msg/internal/redis"
	"github.com/emilhauk/msg/internal/storage"
)

const maxTranscodeBytes = 100 << 20

// HeartbeatInterval is overridable in tests.
var HeartbeatInterval = 10 * time.Second

// ponytail: in-process semaphore, requests queue behind it; move to a job queue if uploads pile up.
var transcodeSem = make(chan struct{}, 2)

// FFmpegPath is overridable in tests.
var FFmpegPath = "ffmpeg"

// TranscodeHandler re-encodes browser-unfriendly videos (HEVC .mov) to H.264 MP4 and stores them in S3.
type TranscodeHandler struct {
	Redis *redisclient.Client
	S3    *storage.S3Client
}

// HandleTranscode handles POST /rooms/{id}/transcode. Body is the raw video bytes;
// response is the attachment JSON the client submits with the message.
func (h *TranscodeHandler) HandleTranscode(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	user := middleware.UserFromContext(r.Context())

	ok, err := h.Redis.IsRoomAccessible(r.Context(), roomID, user.ID)
	if err != nil || !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "video/") {
		http.Error(w, "unsupported content type", http.StatusBadRequest)
		return
	}
	if r.ContentLength > maxTranscodeBytes {
		http.Error(w, fmt.Sprintf("file exceeds maximum allowed size of %d MiB", maxTranscodeBytes>>20), http.StatusRequestEntityTooLarge)
		return
	}

	dir, err := os.MkdirTemp("", "transcode")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "in")
	out := filepath.Join(dir, "out.mp4")

	f, err := os.Create(in)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, maxTranscodeBytes))
	f.Close()
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "bad request", http.StatusBadRequest)
		}
		return
	}

	select {
	case transcodeSem <- struct{}{}:
		defer func() { <-transcodeSem }()
	case <-r.Context().Done():
		return
	}

	// ponytail: newline heartbeats keep iOS Safari's 60 s idle timeout from killing the request mid-encode; job queue + polling if this outgrows one request.
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	flush()

	result := make(chan transcodeResult, 1)
	go func() { result <- h.encode(r.Context(), roomID, user.ID, in, out) }()
	tick := time.NewTicker(HeartbeatInterval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			_, _ = io.WriteString(w, "\n")
			flush()
		case res := <-result:
			_ = json.NewEncoder(w).Encode(res)
			return
		}
	}
}

type transcodeResult struct {
	model.Attachment
	Error string `json:"error,omitempty"`
}

func (h *TranscodeHandler) encode(ctx context.Context, roomID, userID, in, out string) transcodeResult {
	cmd := exec.CommandContext(ctx, FFmpegPath, "-y", "-i", in,
		"-vf", "scale='min(1920,iw)':-2",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p", "-threads", "4",
		"-c:a", "aac", "-movflags", "+faststart", out)
	if output, err := cmd.CombinedOutput(); err != nil {
		log.Ctx(ctx).Warn().Err(err).Str("ffmpeg", string(output)).Msg("transcode failed")
		return transcodeResult{Error: "could not transcode video"}
	}

	of, err := os.Open(out)
	if err != nil {
		return transcodeResult{Error: "internal error"}
	}
	defer of.Close()
	st, err := of.Stat()
	if err != nil {
		return transcodeResult{Error: "internal error"}
	}

	var b [6]byte
	_, _ = rand.Read(b[:])
	hash := hex.EncodeToString(b[:])
	key := storage.MediaKey(roomID, fmt.Sprintf("%d-%s", time.Now().UnixMilli(), userID), hash+".mp4")
	if err := h.S3.PutObject(ctx, key, "video/mp4", of, st.Size()); err != nil {
		log.Ctx(ctx).Error().Err(err).Msg("transcode: upload")
		return transcodeResult{Error: "upload failed"}
	}
	return transcodeResult{Attachment: model.Attachment{URL: h.S3.PublicURL(key), ContentType: "video/mp4", Filename: hash}}
}
