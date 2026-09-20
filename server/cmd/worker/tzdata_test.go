package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimezoneData(t *testing.T) {
	// 시험을 돌리는 기계에는 운영체제의 시간대 파일이 있다. 그래서 time.LoadLocation을 불러 보는 것으로는
	// 자료가 실행 파일에 들어갔는지, 운영체제의 것을 읽었는지 가릴 수 없다.
	// 대신 빌드 태그 없이 빌드할 때 이 실행 파일에 묶이는 패키지를 go 명령에 물어본다.
	t.Run("빌드 태그 없이 빌드해도 시간대 자료가 실행 파일에 들어간다", func(t *testing.T) {
		goBin, err := exec.LookPath("go")
		require.NoError(t, err, "go 명령이 있어야 한다")

		cmd := exec.CommandContext(t.Context(), goBin, "list", "-deps", "-f", "{{.ImportPath}}", ".")
		// 환경에 -tags=timetzdata가 들어 있으면 가져오기가 빠져도 통과해 버린다. 기본 빌드 그대로 본다.
		cmd.Env = append(os.Environ(), "GOFLAGS=")
		out, err := cmd.Output()
		require.NoError(t, err)

		assert.Contains(t, strings.Split(strings.TrimSpace(string(out)), "\n"), "time/tzdata",
			"main.go에서 time/tzdata 가져오기가 빠졌다")
	})
}
