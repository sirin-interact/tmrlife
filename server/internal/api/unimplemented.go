package api

import (
	"context"
	"net/http"
)

// 명세에는 있지만 아직 구현하지 않은 경로다. 명세가 먼저 나가야 웹이 같은 모양을 보고 나란히 만들 수 있어서,
// 구현보다 앞서 자리를 잡아 둔다. 구현이 들어오면 그 메서드를 여기서 지우고, 다 지워지면 이 파일도 지운다.
//
// 501로 답한다. 로그인 확인과 다른 출처 막기, 명세 검증은 이미 걸려 있으므로 구현은 핸들러만 채우면 된다.
// 아직 없는 기능을 위해 오류의 종류를 새로 만들지 않는다. 화면이 안내 문구를 고를 일이 없는 상태다.
var errNotImplemented = newProblem(http.StatusNotImplemented, ProblemCodeInternalError)

func (h *handlers) ListDiaries(context.Context, ListDiariesRequestObject) (ListDiariesResponseObject, error) {
	return nil, errNotImplemented
}

func (h *handlers) GetDiary(context.Context, GetDiaryRequestObject) (GetDiaryResponseObject, error) {
	return nil, errNotImplemented
}

func (h *handlers) PutDiary(context.Context, PutDiaryRequestObject) (PutDiaryResponseObject, error) {
	return nil, errNotImplemented
}

func (h *handlers) DeleteDay(context.Context, DeleteDayRequestObject) (DeleteDayResponseObject, error) {
	return nil, errNotImplemented
}

func (h *handlers) ListResources(context.Context, ListResourcesRequestObject) (ListResourcesResponseObject, error) {
	return nil, errNotImplemented
}
