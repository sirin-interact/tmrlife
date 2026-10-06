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

// 근거 화면(하루의 판단과 그 근거가 된 발화)이 읽는 값.
export type SignalStatus = Schemas['SignalStatus'];
export type SignalExplicitness = Schemas['SignalExplicitness'];
export type SignalEvidence = Schemas['SignalEvidence'];
export type DaySignalItem = Schemas['DaySignalItem'];
export type DaySignals = Schemas['DaySignals'];

// 추세 화면(점 달력)이 읽는 값. SignalItem은 근거 화면도 함께 쓴다.
export type SignalItem = Schemas['SignalItem'];
export type SignalRate = Schemas['SignalRate'];
export type Trend = Schemas['Trend'];
export type TrendRow = Schemas['TrendRow'];
export type TrendCell = Schemas['TrendCell'];
export type TrendRowKey = Schemas['TrendRowKey'];
export type TrendMark = Schemas['TrendMark'];
export type TrendComparison = Schemas['TrendComparison'];

// 계산이 그 값에 이른 과정을 그대로 보여 주는 내부 화면이 읽는 값.
export type InternalReview = Schemas['InternalReview'];
export type ReviewExtractor = Schemas['ReviewExtractor'];
export type ReviewParams = Schemas['ReviewParams'];
export type ReviewScore = Schemas['ReviewScore'];
export type ReviewScoreItem = Schemas['ReviewScoreItem'];
export type ReviewConfidence = Schemas['ReviewConfidence'];
export type ReviewRatio = Schemas['ReviewRatio'];
export type ReviewBaseline = Schemas['ReviewBaseline'];
export type ReviewChange = Schemas['ReviewChange'];
export type ReviewChangePoint = Schemas['ReviewChangePoint'];
export type ReviewStage = Schemas['ReviewStage'];
export type ReviewStagePoint = Schemas['ReviewStagePoint'];
export type ScoreBand = Schemas['ScoreBand'];
export type ConfidenceLevel = Schemas['ConfidenceLevel'];
export type ConfidenceComponent = Schemas['ConfidenceComponent'];
export type StageReason = Schemas['StageReason'];

// 대화 채널(WebSocket)로 오가는 메시지. REST 경로에는 나오지 않지만 같은 명세에서 만들어진다.
export type WsClientMessage = Schemas['WsClientMessage'];
export type WsServerMessage = Schemas['WsServerMessage'];
export type WsUtterance = Schemas['WsUtterance'];
export type WsSpeaker = Schemas['WsSpeaker'];
export type WsOrigin = Schemas['WsOrigin'];
export type WsEndReason = Schemas['WsEndReason'];
export type WsErrorCode = Schemas['WsErrorCode'];
export type WsAudioEndReason = Schemas['WsAudioEndReason'];
export type ConversationMode = Schemas['ConversationMode'];
