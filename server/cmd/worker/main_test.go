package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

// syncBuffer는 작업자가 쓰는 동안 시험이 읽어도 되는 출력 버퍼다.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

var testKEK = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x2a}, 32))

// 시험에서는 운영체제가 고른 포트를 쓴다. 기본 포트는 개발 중인 작업자가 이미 쓰고 있을 수 있다.
const testHealthAddr = "127.0.0.1:0"

var healthListeningPattern = regexp.MustCompile(`"msg":"health listener listening"[^\n]*"addr":"([^"]+)"`)

// healthStatus는 상태 확인 주소에 물어본 결과다. 닿지 않으면 0이다.
func healthStatus(t *testing.T, addr string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/healthz", nil)
	require.NoError(t, err)
	req.Close = true
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestRun(t *testing.T) {
	t.Run("뜨자마자 끝난 세션을 지우는 작업을 한 번 돌리고, 종료 신호에 깨끗하게 내려간다", func(t *testing.T) {
		pool := testdb.New(t)
		t.Setenv("DATABASE_URL", pool.Config().ConnString())
		t.Setenv("DATA_KEK_V1", testKEK)
		t.Setenv("APP_ENV", "test")
		t.Setenv("LOG_LEVEL", "info")
		t.Setenv(healthAddrEnv, testHealthAddr)

		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		stdout, stderr := &syncBuffer{}, &syncBuffer{}
		exit := make(chan int, 1)
		go func() { exit <- run(ctx, stdout, stderr) }()

		require.Eventually(t, func() bool {
			return strings.Contains(stdout.String(), `"msg":"expired sessions deleted"`)
		}, 60*time.Second, 50*time.Millisecond, "주기 작업이 돌지 않았다: %s%s", stdout, stderr)

		// 큐가 도는 동안에는 상태 확인이 200이다. 쿠버네티스의 검사가 이 응답을 본다.
		match := healthListeningPattern.FindStringSubmatch(stdout.String())
		require.NotNil(t, match, "상태 확인 주소가 로그에 없다: %s", stdout)
		healthAddress := match[1]
		assert.Equal(t, http.StatusOK, healthStatus(t, healthAddress))

		stop()
		select {
		case code := <-exit:
			assert.Equal(t, exitOK, code)
		case <-time.After(60 * time.Second):
			t.Fatal("작업자가 내려가지 않았다")
		}
		assert.Zero(t, healthStatus(t, healthAddress), "내려간 뒤에도 상태 확인 포트가 열려 있다")

		logs := stdout.String()
		assert.Contains(t, logs, `"app":"naeil-worker"`)
		assert.Contains(t, logs, `"msg":"worker started"`)
		assert.Contains(t, logs, `"msg":"worker stopped"`)
		assert.NotContains(t, logs, testKEK)
		assert.Empty(t, stderr.String())

		var app string
		require.NoError(t, pool.QueryRow(t.Context(),
			`SELECT count(*)::text FROM river_job WHERE kind = 'auth_session_cleanup' AND state = 'completed'`).Scan(&app))
		assert.Equal(t, "1", app)
	})

	t.Run("설정이 틀리면 띄우지 않고 변수 이름만 알려준 뒤 2로 끝난다", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		t.Setenv("DATA_KEK_V1", "not-a-valid-key-but-still-secret")

		var stdout, stderr bytes.Buffer
		code := run(t.Context(), &stdout, &stderr)

		assert.Equal(t, exitUsage, code)
		assert.Contains(t, stderr.String(), "DATABASE_URL")
		assert.Contains(t, stderr.String(), "DATA_KEK_V1")
		assert.NotContains(t, stderr.String(), "not-a-valid-key-but-still-secret")
		assert.Empty(t, stdout.String())
	})

	t.Run("상태 확인 주소가 틀리면 다른 설정 오류와 함께 알려주고 2로 끝난다", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		t.Setenv("DATA_KEK_V1", testKEK)
		t.Setenv(healthAddrEnv, "not-an-address-but-still-private")

		var stdout, stderr bytes.Buffer
		code := run(t.Context(), &stdout, &stderr)

		assert.Equal(t, exitUsage, code)
		assert.Contains(t, stderr.String(), "DATABASE_URL")
		assert.Contains(t, stderr.String(), healthAddrEnv)
		assert.NotContains(t, stderr.String(), "not-an-address-but-still-private")
		assert.Empty(t, stdout.String())
	})

	t.Run("상태 확인 포트를 열지 못하면 큐를 시작하지 않고 1로 끝난다", func(t *testing.T) {
		busy, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", testHealthAddr)
		require.NoError(t, err)
		defer busy.Close()

		pool := testdb.New(t)
		t.Setenv("DATABASE_URL", pool.Config().ConnString())
		t.Setenv("DATA_KEK_V1", testKEK)
		t.Setenv("APP_ENV", "test")
		t.Setenv(healthAddrEnv, busy.Addr().String())

		var stdout, stderr bytes.Buffer
		code := run(t.Context(), &stdout, &stderr)

		assert.Equal(t, exitFailure, code)
		assert.Contains(t, stdout.String(), "open health listener")
		assert.NotContains(t, stdout.String(), `"msg":"worker started"`)
	})

	t.Run("DB에 닿지 않으면 1로 끝나고 비밀번호는 로그에 남지 않는다", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://app:s3cr3t-pass@127.0.0.1:1/app?sslmode=disable&connect_timeout=2")
		t.Setenv("DATA_KEK_V1", testKEK)
		t.Setenv("APP_ENV", "test")
		t.Setenv(healthAddrEnv, testHealthAddr)

		var stdout, stderr bytes.Buffer
		code := run(t.Context(), &stdout, &stderr)

		assert.Equal(t, exitFailure, code)
		assert.Contains(t, stdout.String(), "start queue client")
		assert.NotContains(t, stdout.String(), "s3cr3t-pass")
	})
}
