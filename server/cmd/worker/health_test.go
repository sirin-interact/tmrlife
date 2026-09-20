package main

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthAddr(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "비어 있으면 기본 포트를 쓴다", value: "", want: defaultHealthAddr},
		{name: "공백만 있어도 비어 있는 것으로 본다", value: "  ", want: defaultHealthAddr},
		{name: "포트만 적을 수 있다", value: ":9090", want: ":9090"},
		{name: "앞뒤 공백은 뗀다", value: " :9090 ", want: ":9090"},
		{name: "주소와 포트를 함께 적을 수 있다", value: "127.0.0.1:0", want: "127.0.0.1:0"},
		{name: "IPv6 주소도 받는다", value: "[::1]:8081", want: "[::1]:8081"},
		{name: "포트만 숫자로 적으면 받지 않는다", value: "8081", wantErr: true},
		{name: "포트가 빠지면 받지 않는다", value: "localhost:", wantErr: true},
		{name: "포트가 숫자가 아니면 받지 않는다", value: ":health-secret", wantErr: true},
		{name: "포트가 범위를 넘으면 받지 않는다", value: ":65536", wantErr: true},
		{name: "음수 포트는 받지 않는다", value: ":-1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := healthAddr(tt.value)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), healthAddrEnv)
				// 다른 설정 오류와 같이 값은 되돌려 보여주지 않는다.
				assert.NotContains(t, err.Error(), "secret")
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHealthListener(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	get := func(t *testing.T, method, url string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
		require.NoError(t, err)
		// 연결을 재사용하지 않는다. 닫힌 뒤의 확인이 남은 연결을 타지 않게 한다.
		req.Close = true
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(body)
	}

	t.Run("큐가 도는 동안에만 200으로 답한다", func(t *testing.T) {
		h, err := listenHealth(t.Context(), "127.0.0.1:0", logger)
		require.NoError(t, err)
		defer h.Close(t.Context())
		url := "http://" + h.Addr() + "/healthz"

		// 위에서 아래로 이어지는 한 번의 흐름이다. 앞 단계가 남긴 상태에서 다음 단계를 본다.
		tests := []struct {
			name       string
			change     func()
			wantStatus int
			wantBody   string
		}{
			{name: "큐를 시작하기 전에는 503", change: func() {}, wantStatus: http.StatusServiceUnavailable, wantBody: "queue client is not running\n"},
			{name: "큐가 돌면 200", change: func() { h.SetRunning(true) }, wantStatus: http.StatusOK, wantBody: "ok\n"},
			{name: "큐가 멈추면 다시 503", change: func() { h.SetRunning(false) }, wantStatus: http.StatusServiceUnavailable, wantBody: "queue client is not running\n"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				tt.change()
				status, body := get(t, http.MethodGet, url)
				assert.Equal(t, tt.wantStatus, status)
				assert.Equal(t, tt.wantBody, body)
			})
		}
	})

	t.Run("상태 확인 말고는 아무것도 받지 않는다", func(t *testing.T) {
		h, err := listenHealth(t.Context(), "127.0.0.1:0", logger)
		require.NoError(t, err)
		defer h.Close(t.Context())
		h.SetRunning(true)

		tests := []struct {
			name       string
			method     string
			path       string
			wantStatus int
		}{
			{name: "다른 경로는 404", method: http.MethodGet, path: "/", wantStatus: http.StatusNotFound},
			{name: "준비 확인 경로를 따로 두지 않는다", method: http.MethodGet, path: "/readyz", wantStatus: http.StatusNotFound},
			{name: "GET이 아니면 405", method: http.MethodPost, path: "/healthz", wantStatus: http.StatusMethodNotAllowed},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				status, _ := get(t, tt.method, "http://"+h.Addr()+tt.path)
				assert.Equal(t, tt.wantStatus, status)
			})
		}
	})

	t.Run("닫으면 포트를 돌려준다", func(t *testing.T) {
		h, err := listenHealth(t.Context(), "127.0.0.1:0", logger)
		require.NoError(t, err)
		addr := h.Addr()
		h.Close(t.Context())

		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", addr)
		require.NoError(t, err, "닫은 뒤에도 포트가 잡혀 있다")
		require.NoError(t, ln.Close())
	})

	t.Run("이미 쓰이는 포트면 큐를 시작하기 전에 오류로 알려준다", func(t *testing.T) {
		busy, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer busy.Close()

		h, err := listenHealth(t.Context(), busy.Addr().String(), logger)
		require.Error(t, err)
		assert.Nil(t, h)
	})
}
