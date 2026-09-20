package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

const (
	// firstLineRunes는 목록에 싣는 첫 줄의 최대 글자 수다. 화면이 한 줄로 보여줄 만큼만 보낸다.
	firstLineRunes = 60

	// snippetContextRunes는 검색 결과에서 맞은 자리의 앞뒤로 함께 보내는 글자 수다.
	snippetContextRunes = 30

	// maxSearchScan은 검색 한 번에 읽어 와 풀어 볼 일기의 최대 개수다. 읽어 오는 쿼리의 상한도 이 값이다.
	//
	// 일기 글은 암호문이라 DB에서 찾을 수 없다. 본인의 일기를 읽어 풀어서 메모리에서 찾는 수밖에 없는데,
	// 그 값은 일기 수에 비례해 늘어난다. 하루에 하나이므로 이 값은 여덟 해가 넘는 기록이다.
	// 그보다 오래 쓴 사용자가 생기면 검색을 DB 쪽으로 옮겨야 한다(찾을 수 있는 색인을 따로 두는 방법).
	// 그때까지는 최근 것부터 이만큼만 보고, 더 오래된 것은 찾지 않는다.
	maxSearchScan = 3000

	// maxSearchResults는 한 번에 돌려주는 검색 결과의 최대 개수다. 화면은 최근 것부터 보여준다.
	maxSearchResults = 100

	// maxQueryRunes는 받아 주는 검색어의 최대 글자 수다. 명세의 한도와 같다.
	// 검증기가 먼저 거르지만, 검증을 거치지 않는 길이 생겨도 여기서 막는다.
	maxQueryRunes = 100
)

// diaryService는 일기 경로가 저장소와 암복호를 다루는 자리다.
// 핸들러는 요청을 옮기고 응답을 만들 뿐, 기록을 푸는 일은 모두 여기서 한다.
type diaryService struct {
	store   *store.Store
	sealers *sealing.Sealers
	clock   clock.Clock
}

// ListDiaries는 한 달의 목록이나 검색 결과를 돌려준다. month와 q 가운데 하나만 받는다.
func (h *handlers) ListDiaries(ctx context.Context, request ListDiariesRequestObject) (ListDiariesResponseObject, error) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	month, query := request.Params.Month, request.Params.Q
	if (month == nil) == (query == nil) {
		// 둘 다 왔거나 둘 다 오지 않았다. 무엇을 보여줄지 서버가 대신 고르지 않는다.
		return nil, newProblem(http.StatusBadRequest, ProblemCodeValidationFailed)
	}

	if month != nil {
		items, err := h.diaries.listMonth(ctx, principal.User.ID, *month)
		if err != nil {
			return nil, err
		}
		return ListDiaries200JSONResponse{Items: items}, nil
	}

	items, err := h.diaries.search(ctx, principal.User.ID, *query)
	if err != nil {
		return nil, err
	}
	return ListDiaries200JSONResponse{Items: items}, nil
}

// GetDiary는 그날의 일기를 돌려준다. 남의 날짜는 없는 것과 똑같이 404다.
func (h *handlers) GetDiary(ctx context.Context, request GetDiaryRequestObject) (GetDiaryResponseObject, error) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	date, err := recordDateOf(request.Date)
	if err != nil {
		return nil, err
	}
	on, err := pgDate(date)
	if err != nil {
		return nil, err
	}

	row, err := h.diaries.store.Queries().GetDiaryByDate(ctx, db.GetDiaryByDateParams{
		UserID: principal.User.ID, RecordDate: on,
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, errDiaryNotFound
	case err != nil:
		return nil, fmt.Errorf("get diary: %w", err)
	}

	text, err := h.diaries.openVisible(ctx, principal.User.ID, row)
	if err != nil {
		return nil, err
	}
	return GetDiary200JSONResponse(diaryResponse(date, row, text)), nil
}

// PutDiary는 사용자가 고친 글을 그날의 일기로 확인한다. 바뀌는 것은 일기 글뿐이고 마음 신호는 그대로다.
//
// # 뒤늦게 올라온 초안과 부딪히는 문제
//
// 이 경로에는 "내가 읽은 뒤로 바뀌었는가"를 가리는 수단이 없다. 사용자가 일기를 열어 둔 사이에 그날 다시 대화하면
// 초안 작업이 새 초안을 올리는데, 사용자가 열어 둔 옛 글로 저장하면 그 새 초안이 소리 없이 지워진다.
// 지금은 명세대로 마지막에 저장한 글이 남는다. 제대로 고치려면 저장소(DiaryBodyWrite에 읽었을 때의 updated_at),
// 명세(ETag나 If-Match, 그리고 409), 웹이 함께 바뀌어야 하고 그것은 이 작업의 범위 밖이다.
// 응답의 updated_at은 이미 나가고 있으므로, 조건부 저장을 붙일 때 클라이언트가 실어 보낼 값은 준비되어 있다.
func (h *handlers) PutDiary(ctx context.Context, request PutDiaryRequestObject) (PutDiaryResponseObject, error) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	if request.Body == nil {
		return nil, errUnreadableBody
	}
	date, err := recordDateOf(request.Date)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(request.Body.Text)
	if text == "" {
		return nil, invalidField(ProblemFieldText)
	}

	row, err := h.diaries.saveBody(ctx, principal.User.ID, date, text)
	if err != nil {
		return nil, err
	}
	return PutDiary200JSONResponse(diaryResponse(date, row, text)), nil
}

// DeleteDay는 하루를 통째로 지운다. 그날의 대화, 발화, 관문 기록, 일기, 신호, 그날에서 나온 기억이 외래 키를 따라 함께 지워진다.
func (h *handlers) DeleteDay(ctx context.Context, request DeleteDayRequestObject) (DeleteDayResponseObject, error) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	date, err := recordDateOf(request.Date)
	if err != nil {
		return nil, err
	}
	on, err := pgDate(date)
	if err != nil {
		return nil, err
	}

	_, err = h.diaries.store.Queries().DeleteDayByDate(ctx, db.DeleteDayByDateParams{
		UserID: principal.User.ID, RecordDate: on,
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, errDiaryNotFound
	case err != nil:
		return nil, fmt.Errorf("delete day: %w", err)
	}
	return DeleteDay204Response{}, nil
}

// ListResources는 도움이 필요할 때 연락할 곳을 돌려준다. 로그인하지 않아도 열린다.
func (h *handlers) ListResources(context.Context, ListResourcesRequestObject) (ListResourcesResponseObject, error) {
	return ListResources200JSONResponse{Items: resourceList(h.phrases.UrgentResources())}, nil
}

// errDiaryNotFound는 그 날짜에 찾는 기록이 없다는 뜻이다. 남의 기록도 이것으로 답한다.
var errDiaryNotFound = newProblem(http.StatusNotFound, ProblemCodeNotFound)

// listMonth는 그 달의 일기를 날짜순으로 돌려준다. month는 YYYY-MM이다.
func (s *diaryService) listMonth(ctx context.Context, userID uuid.UUID, month string) ([]DiarySummary, error) {
	from, to, err := monthBounds(month)
	if err != nil {
		return nil, err
	}
	fromDate, err := pgDate(from)
	if err != nil {
		return nil, err
	}
	toDate, err := pgDate(to)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Queries().ListDiariesByDateRange(ctx, db.ListDiariesByDateRangeParams{
		UserID: userID, FromDate: fromDate, ToDate: toDate,
	})
	if err != nil {
		return nil, fmt.Errorf("list diaries: %w", err)
	}
	if len(rows) == 0 {
		return []DiarySummary{}, nil
	}

	sealer, err := s.sealerFor(ctx, userID)
	if err != nil {
		return nil, err
	}
	items := make([]DiarySummary, 0, len(rows))
	for _, row := range rows {
		date, err := store.RecordDate(row.RecordDate)
		if err != nil {
			return nil, fmt.Errorf("read record date: %w", err)
		}
		// 글 하나가 열리지 않는다고 그 달을 통째로 막지 않는다. 그 일기만 빈 첫 줄로 보인다.
		text, _ := openVisible(sealer, row.Diary)
		items = append(items, DiarySummary{
			Date:      apiDate(date),
			Status:    DiaryStatus(row.Diary.Status),
			FirstLine: firstLine(text),
		})
	}
	return items, nil
}

// search는 본인의 일기를 풀어서 메모리에서 찾는다. 최근 날짜부터 보고, 맞은 자리의 앞뒤 글을 함께 담는다.
func (s *diaryService) search(ctx context.Context, userID uuid.UUID, raw string) ([]DiarySummary, error) {
	query := strings.TrimSpace(raw)
	switch {
	case query == "":
		return nil, invalidField(ProblemFieldQ)
	case len([]rune(query)) > maxQueryRunes:
		return nil, invalidField(ProblemFieldQ)
	}

	rows, err := s.store.Queries().ListDiariesByUser(ctx, db.ListDiariesByUserParams{
		UserID: userID, MaxRows: maxSearchScan,
	})
	if err != nil {
		return nil, fmt.Errorf("list diaries: %w", err)
	}
	if len(rows) == 0 {
		return []DiarySummary{}, nil
	}

	sealer, err := s.sealerFor(ctx, userID)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(query)
	items := make([]DiarySummary, 0, min(len(rows), maxSearchResults))
	for i, row := range rows {
		if i >= maxSearchScan || len(items) >= maxSearchResults {
			break
		}
		text, ok := openVisible(sealer, row.Diary)
		if !ok {
			continue
		}
		at := strings.Index(strings.ToLower(text), needle)
		if at < 0 {
			continue
		}
		date, err := store.RecordDate(row.RecordDate)
		if err != nil {
			return nil, fmt.Errorf("read record date: %w", err)
		}
		snippet := snippetAround(text, at, len(query))
		items = append(items, DiarySummary{
			Date:      apiDate(date),
			Status:    DiaryStatus(row.Diary.Status),
			FirstLine: firstLine(text),
			Snippet:   &snippet,
		})
	}
	return items, nil
}

// saveBody는 고친 글을 그날의 일기로 저장한다. 대화하지 않은 날이면 찾지 못함이다.
func (s *diaryService) saveBody(ctx context.Context, userID uuid.UUID, date recorddate.Date, text string) (db.Diary, error) {
	sealer, err := s.sealerFor(ctx, userID)
	if err != nil {
		return db.Diary{}, err
	}
	on, err := pgDate(date)
	if err != nil {
		return db.Diary{}, err
	}
	newID, err := store.NewID()
	if err != nil {
		return db.Diary{}, fmt.Errorf("new diary id: %w", err)
	}
	now := s.clock.Now()

	var saved db.Diary
	err = s.store.InTx(ctx, func(q *db.Queries) error {
		day, err := q.GetDayByDate(ctx, db.GetDayByDateParams{UserID: userID, RecordDate: on})
		switch {
		case errors.Is(err, store.ErrNotFound):
			return errDiaryNotFound
		case err != nil:
			return fmt.Errorf("get day: %w", err)
		}

		saved, err = store.SaveDiaryBody(ctx, q, store.DiaryBodyWrite{
			NewID:  newID,
			DayID:  day.ID,
			UserID: userID,
			Seal: func(rowID uuid.UUID) ([]byte, error) {
				return sealer.SealString(text, sealing.DiaryBody(rowID))
			},
			Now: now,
		})
		if errors.Is(err, store.ErrNotFound) {
			// 저장하는 사이에 그날을 지웠다.
			return errDiaryNotFound
		}
		return err
	})
	if err != nil {
		return db.Diary{}, err
	}
	return saved, nil
}

// sealerFor는 그 사용자의 Sealer를 받아 온다. 계정이 지워졌으면 로그인한 사람이 없는 것과 같다.
func (s *diaryService) sealerFor(ctx context.Context, userID uuid.UUID) (*crypto.Sealer, error) {
	sealer, err := s.sealers.For(ctx, userID)
	switch {
	case errors.Is(err, sealing.ErrNoKey):
		return nil, errUnauthenticated
	case err != nil:
		return nil, fmt.Errorf("open user key: %w", err)
	}
	return sealer, nil
}

// openVisible은 지금 화면에 보일 글을 연다. 확인한 일기면 확인한 글, 초안이면 초안이다.
func (s *diaryService) openVisible(ctx context.Context, userID uuid.UUID, row db.Diary) (string, error) {
	sealer, err := s.sealerFor(ctx, userID)
	if err != nil {
		return "", err
	}
	text, ok := openVisible(sealer, row)
	if !ok {
		// 글을 열 수 없다. 사용자에게는 빈 글로 보이고, 그대로 저장하면 그 글이 일기가 된다.
		// 여기서 오류를 내면 그날의 일기 화면이 영영 열리지 않는다.
		return "", nil
	}
	return text, nil
}

func openVisible(sealer *crypto.Sealer, row db.Diary) (string, bool) {
	sealed, aad := row.BodyEnc, sealing.DiaryBody(row.ID)
	if row.Status != store.DiaryConfirmed {
		sealed, aad = row.DraftEnc, sealing.DiaryDraft(row.ID)
	}
	if len(sealed) == 0 {
		return "", true
	}
	text, err := sealer.OpenString(sealed, aad)
	if err != nil {
		return "", false
	}
	return text, true
}

func diaryResponse(date recorddate.Date, row db.Diary, text string) Diary {
	out := Diary{
		Date:      apiDate(date),
		Status:    DiaryStatus(row.Status),
		Text:      text,
		UpdatedAt: NewUTCTime(row.UpdatedAt),
	}
	if row.ConfirmedAt != nil {
		confirmed := NewUTCTime(*row.ConfirmedAt)
		out.ConfirmedAt = &confirmed
	}
	return out
}

func resourceList(items []phrases.Resource) []Resource {
	out := make([]Resource, 0, len(items))
	for _, r := range items {
		out = append(out, Resource{ID: r.ID, Name: r.Name, Phone: r.Phone, Description: r.Description})
	}
	return out
}

// apiDate는 기록 날짜를 응답에 싣는 꼴로 바꾼다. 날짜만 쓰는 값이라 자정의 UTC 시각으로 담는다.
func apiDate(d recorddate.Date) RecordDate {
	return RecordDate{Time: d.UTCMidnight()}
}

// pgDate는 기록 날짜를 쿼리에 넘길 꼴로 바꾼다.
// 여기까지 오는 날짜는 모두 검사를 거친 값이라 빈 날짜일 수 없다. 그래도 조용히 넘기지 않고 오류로 돌린다.
func pgDate(d recorddate.Date) (pgtype.Date, error) {
	value, err := store.PGDate(d)
	if err != nil {
		return pgtype.Date{}, fmt.Errorf("record date: %w", err)
	}
	return value, nil
}

// firstLine은 글의 첫 줄을 잘라서 돌려준다. 빈 초안이면 빈 문자열이다.
func firstLine(text string) string {
	line := text
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	return truncate(strings.TrimSpace(line), firstLineRunes)
}

// snippetAround는 맞은 자리의 앞뒤 글을 잘라 돌려준다. 잘린 쪽에는 줄임표를 붙인다.
func snippetAround(text string, at, length int) string {
	runes := []rune(text)
	// 바이트 자리를 글자 자리로 옮긴다.
	start := len([]rune(text[:at]))
	end := start + len([]rune(text[at:at+length]))

	from := max(start-snippetContextRunes, 0)
	to := min(end+snippetContextRunes, len(runes))

	var b strings.Builder
	if from > 0 {
		b.WriteString("…")
	}
	b.WriteString(strings.TrimSpace(strings.ReplaceAll(string(runes[from:to]), "\n", " ")))
	if to < len(runes) {
		b.WriteString("…")
	}
	return b.String()
}

func truncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

// monthBounds는 YYYY-MM을 그 달 1일과 다음 달 1일로 바꾼다.
func monthBounds(month string) (from, to recorddate.Date, err error) {
	t, parseErr := time.Parse("2006-01", month)
	if parseErr != nil {
		// 검증기가 이미 꼴을 본다. 여기까지 왔다면 검증을 거치지 않은 길이 생긴 것이다.
		return recorddate.Date{}, recorddate.Date{}, newProblem(http.StatusBadRequest, ProblemCodeValidationFailed)
	}
	from, dateErr := recorddate.New(t.Year(), t.Month(), 1)
	if dateErr != nil {
		return recorddate.Date{}, recorddate.Date{}, newProblem(http.StatusBadRequest, ProblemCodeValidationFailed)
	}
	next := t.AddDate(0, 1, 0)
	to, dateErr = recorddate.New(next.Year(), next.Month(), 1)
	if dateErr != nil {
		return recorddate.Date{}, recorddate.Date{}, newProblem(http.StatusBadRequest, ProblemCodeValidationFailed)
	}
	return from, to, nil
}

// recordDateOf는 경로에서 온 날짜를 기록 날짜로 바꾼다.
// 명세의 검증기가 이미 꼴과 실제로 있는 날짜인지를 본다. 그래도 값을 믿지 않고 한 번 더 가른다.
func recordDateOf(in RecordDate) (recorddate.Date, error) {
	date, err := recorddate.New(in.Year(), in.Month(), in.Day())
	if err != nil {
		return recorddate.Date{}, newProblem(http.StatusBadRequest, ProblemCodeValidationFailed)
	}
	return date, nil
}
