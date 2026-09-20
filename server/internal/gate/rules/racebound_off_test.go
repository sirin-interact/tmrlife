//go:build !race

package rules_test

import "time"

// 경쟁 검출기 없이 돌 때의 한계값. 1 MiB짜리 글은 실제로 1\~2초면 끝난다.
const hostileInputBudget = 20 * time.Second
