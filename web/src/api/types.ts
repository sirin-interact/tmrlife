import type { components } from '@/api/schema';

// 생성된 타입의 긴 경로를 화면 코드가 되풀이해 적지 않도록 이름을 붙여 둔다.
// 모양을 여기서 다시 정의하지 않는다. 명세가 바뀌면 이 이름들도 함께 바뀌어야 한다.
type Schemas = components['schemas'];

export type User = Schemas['User'];
export type Me = Schemas['Me'];
export type AuthRequirements = Schemas['AuthRequirements'];
export type ConsentGrant = Schemas['ConsentGrant'];
export type ConsentKind = Schemas['ConsentKind'];
export type SignupRequest = Schemas['SignupRequest'];
export type LoginRequest = Schemas['LoginRequest'];
export type Problem = Schemas['Problem'];
export type ProblemCode = Schemas['ProblemCode'];
export type ProblemField = Schemas['ProblemField'];
export type PasswordReason = Schemas['PasswordReason'];
export type ConsentProblem = Schemas['ConsentProblem'];

export type RecordDate = Schemas['RecordDate'];
export type Diary = Schemas['Diary'];
export type DiaryStatus = Schemas['DiaryStatus'];
export type DiarySummary = Schemas['DiarySummary'];
export type DiaryUpdate = Schemas['DiaryUpdate'];
export type Resource = Schemas['Resource'];

// 대화 채널(WebSocket)로 오가는 메시지. REST 경로에는 나오지 않지만 같은 명세에서 만들어진다.
export type WsClientMessage = Schemas['WsClientMessage'];
export type WsServerMessage = Schemas['WsServerMessage'];
export type WsUtterance = Schemas['WsUtterance'];
export type WsSpeaker = Schemas['WsSpeaker'];
export type WsOrigin = Schemas['WsOrigin'];
export type WsEndReason = Schemas['WsEndReason'];
export type WsErrorCode = Schemas['WsErrorCode'];
