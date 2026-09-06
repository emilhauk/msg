package handler

import (
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

	cmd := exec.CommandContext(r.Context(), FFmpegPath, "-y", "-i", in,
		"-vf", "scale='min(1920,iw)':-2",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-movflags", "+faststart", out)
	if output, err := cmd.CombinedOutput(); err != nil {
		log.Ctx(r.Context()).Warn().Err(err).Str("ffmpeg", string(output)).Msg("transcode failed")
		http.Error(w, "could not transcode video", http.StatusUnprocessableEntity)
		return
	}

	of, err := os.Open(out)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer of.Close()
	st, err := of.Stat()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var b [6]byte
	_, _ = rand.Read(b[:])
	hash := hex.EncodeToString(b[:])
	key := storage.MediaKey(roomID, fmt.Sprintf("%d-%s", time.Now().UnixMilli(), user.ID), hash+".mp4")
	if err := h.S3.PutObject(r.Context(), key, "video/mp4", of, st.Size()); err != nil {
		log.Ctx(r.Context()).Error().Err(err).Msg("transcode: upload")
		http.Error(w, "upload failed", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(model.Attachment{URL: h.S3.PublicURL(key), ContentType: "video/mp4", Filename: hash})
}
