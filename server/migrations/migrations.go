// Package migrations는 goose 마이그레이션 파일을 실행 파일 안에 담는다.
// 배포된 서버가 저장소 없이도 스키마를 올릴 수 있어야 하기 때문이다.
//
// 파일 이름은 00001_이름.sql 꼴로 번호를 이어 붙인다. 이미 배포된 파일은 고치지 않고 새 번호로 더한다.
package migrations

import "embed"

// FS는 이 디렉터리의 모든 SQL 마이그레이션이다.
//
//go:embed *.sql
var FS embed.FS
