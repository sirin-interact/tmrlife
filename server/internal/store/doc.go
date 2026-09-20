// Package store는 PostgreSQL에 닿는 단 하나의 자리다.
//
// SQL은 queries/ 아래에 직접 쓰고, sqlc가 그것을 db 패키지의 Go 코드로 만든다.
// db 패키지는 손으로 고치지 않는다. 쿼리나 마이그레이션을 고치고 `make generate`로 다시 만든다.
//
// 쓰는 쪽이 알아야 할 규칙은 다음과 같다.
//
//   - 시각은 언제나 인자로 넘긴다. 쿼리는 now()를 쓰지 않고 테이블에도 시각의 기본값이 없다.
//     시계를 주입받는 코드와 DB의 시계가 따로 놀면 가짜 시계로 돌리는 시험의 결과가 달라진다.
//   - 새 행의 ID는 NewID로 만들어 넘긴다. 암호문에 행 ID를 묶으므로 행을 쓰기 전에 ID가 정해져 있어야 한다.
//   - 이름이 _enc로 끝나는 컬럼에는 이미 암호화한 값만 넘긴다. 이 패키지는 평문을 받지도, 풀지도 않는다.
//   - 여러 문장을 묶을 때는 Queries.WithTx가 아니라 Store.InTx를 쓴다. 그래야 오류가 이 패키지의 오류 값으로 바뀐다.
//   - 찾는 행이 없으면 ErrNotFound, 유일 제약에 걸리면 ErrConflict가 나온다. errors.Is로 확인한다.
//   - date 컬럼은 pgtype.Date로 주고받는다. 기록 날짜는 새벽의 경계 시각과 사용자 시간대로 정해지는 값이므로,
//     시각(time.Time)에서 날짜 부분을 잘라 만들면 안 된다. recorddate.Of로 계산한 기록 날짜를 PGDate로 바꿔 넘기고,
//     읽은 값은 RecordDate로 되돌린다. NULL일 수 있는 컬럼에는 NullablePGDate와 NullableRecordDate를 쓴다.
//   - 발화는 AppendUtterance로 더한다. 대화를 잡아 둔 채 다음 순번을 읽으므로 동시에 더해도 순번이 겹치거나 비지 않는다.
//   - 일기 글은 SaveDiaryDraft와 SaveDiaryBody로 저장한다. 일기는 하루에 하나라서 새 행인지 있는 행인지를 그날을 잡아 둔 뒤에야 알고,
//     암호문에는 행 ID가 묶인다. 그래서 글 대신 잠그는 함수(SealFunc)를 받아 행이 정해진 뒤에 부른다.
//   - 대화의 상태, 말한 쪽, 출처 같은 열거 값은 이 패키지의 상수를 쓴다. 테이블의 CHECK 제약과 같은 값인지 시험이 견준다.
//
// 제약 위반 오류의 문구(Error)에는 제약 이름까지만 들어 있고 행의 값은 들어 있지 않다.
// 값은 pgconn.PgError의 Detail에 따로 담기므로, 그 필드를 로그에 남기지 않는다.
package store
