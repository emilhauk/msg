package handler_test

import (
	"net/http"
	"testing"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleRoom_Success(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/"+testRoom, nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

func TestHandleRoom_Unauthenticated(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})

	client := testutil.NoRedirectClient()
	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/"+testRoom, nil)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Location"), "/login")
}

func TestHandleRoom_NotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/does-not-exist", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
