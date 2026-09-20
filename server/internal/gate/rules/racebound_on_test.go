//go:build race

package rules_test

import "time"

// 경쟁 검출기는 메모리 접근마다 기록을 남겨 20배 넘게 느려진다. CI는 -race로 돌리므로 한계값을 따로 둔다.
// 여기서 보려는 것은 "글이 길어져도 시간이 폭발하지 않는다"이고, 그것은 아래의 선형성 확인이 맡는다.
const hostileInputBudget = 90 * time.Second
