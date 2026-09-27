package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/analysis"
	"github.com/sirin-interact/tmrlife/server/internal/app"
	"github.com/sirin-interact/tmrlife/server/internal/auth"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// 심을 수 있는 날 수의 한도다. 기본값은 심는 이야기의 길이(seedScript)와 같다.
// 상한을 두는 것은 오타로 들어온 큰 값이 데이터베이스를 채우지 않게 하려는 것이다.
const (
	defaultSeedDays = 28
	maxSeedDays     = 60
	seedTimeout     = 5 * time.Minute
)

// seedDay는 심는 하루다. 그날 사용자가 한 말과, 그날의 일기 한 단락이다.
type seedDay struct {
	// said는 사용자가 한 말이다. 적은 순서대로 저장된다.
	said []string
	// diary는 그날의 일기 초안이다. said에 있는 내용만 1인칭 일기체로 옮긴 글이다.
	diary string
}

// seedScript는 심는 며칠의 대화다. 오래된 날부터 적었고, 마지막 날이 오늘이다(seedDayAt).
//
// 되풀이되는 하루가 아니라 흐름이다. 앞 절반은 신호가 드문 평범한 날들이고, 뒤 절반은 잠과 기분,
// 피로 이야기가 잦아지는 날들이다. 평소를 잡는 기간은 첫 대화 날부터 두 주이고 점수와 추세가 보는 창은
// 마지막 두 주라서, 기본 날 수로 심으면 두 구간이 겹치지 않는다. 같은 하루를 되풀이하는 대본으로 심으면
// 두 구간의 빈도가 구조적으로 같아져서 "평소보다 잦아요"와 "변화 감지"가 한 번도 나오지 않는다.
// 이 서비스가 보여주겠다고 하는 것이 바로 그 둘이므로, 심는 기록은 그 둘이 실제로 켜지는 이야기여야 한다.
//
// 말은 사람이 쓴 문장이고, 어느 항목으로 읽힐지는 여기에 적지 않는다. 심은 발화를 실제 추출 경로
// (analysis.Service)에 그대로 넘기므로, 화면에 보이는 판단과 근거는 모델이 그 말에서 읽어 낸 것이다.
// 판단을 여기서 적어 넣으면 시연 화면이 "추출이 한 일"이 아니라 "심을 때 적어 둔 값"을 보여주게 된다.
// 하루에 대여섯 마디를 적는 것도 같은 까닭이다. 두 마디로는 여덟 항목 가운데 여섯이 "말하지 않음"으로 남아서
// 근거 화면이 추출이 한 일을 보여주지 못한다.
//
// 일기도 사람이 쓴 문단이다. 발화를 이어 붙여 만들지 않는다. 달력에서 지난 하루를 열어 본 사람이 읽는 글이라
// 사람이 쓴 것처럼 읽혀야 하고, 그날 한 말에 없는 내용은 담지 않는다.
//
// 위기 관문에 걸리는 말은 넣지 않는다. 심는 길에는 관문이 없어서 판정 기록이 남지 않고,
// 그러면 관문이 본 적 없는 말이 근거로 쓰일 수 있다. 힘들어지는 이야기이지만 관문이 보는 표현은 쓰지 않는다.
var seedScript = []seedDay{
	{
		said: []string{
			"어젯밤에는 푹 잤어",
			"아침에 몸이 가볍고 상쾌해서 공원을 한 바퀴 걸었어",
			"점심은 김치찌개를 맛있게 먹었어",
			"오후에는 보고서를 집중해서 마무리했어",
			"특별한 일은 없었지만 기분은 괜찮았어",
		},
		diary: "어젯밤에 푹 잤다. 아침에 몸이 가볍고 상쾌해서 공원을 한 바퀴 걸었다. " +
			"점심에는 김치찌개를 맛있게 먹었다. 오후에는 보고서를 집중해서 마무리했다. " +
			"특별한 일은 없었지만 기분은 괜찮았다.",
	},
	{
		said: []string{
			"오늘은 회의가 길었지만 기분이 좋았어",
			"저녁에 친구랑 통화했는데 재밌었어",
			"밥은 잘 먹었어",
			"잠도 잘 잤어",
			"몸이 가볍고 발걸음도 빨랐어",
		},
		diary: "회의가 길었지만 기분이 좋았다. 저녁에 친구와 통화했는데 재밌었다. " +
			"밥도 잘 먹었고 잠도 잘 잤다. 몸이 가볍고 발걸음도 빨랐다.",
	},
	{
		said: []string{
			"어젯밤에 두 번 깼어",
			"그래도 낮에는 기운이 났어",
			"점심은 잘 먹었어",
			"일은 집중해서 했어",
			"기분은 괜찮았어",
		},
		diary: "어젯밤에 두 번 깼다. 그래도 낮에는 기운이 났다. " +
			"점심을 잘 먹었고 일도 집중해서 했다. 기분은 괜찮았다.",
	},
	{
		said: []string{
			"오늘은 도서관에서 소설을 읽었는데 재밌었어",
			"읽는 동안 집중이 잘 됐어",
			"저녁은 국수를 맛있게 먹었어",
			"밤에는 잘 잤어",
			"기분도 괜찮았어",
		},
		diary: "도서관에서 소설을 읽었는데 재밌었다. 읽는 동안 집중이 잘 됐다. " +
			"저녁에는 국수를 맛있게 먹었다. 밤에 잘 잤고 기분도 괜찮았다.",
	},
	{
		said: []string{
			"오늘은 일이 많아서 바빴지만 기분은 괜찮았어",
			"일하는 동안은 집중해서 했어",
			"퇴근하고 나니 피곤했어",
			"그래도 저녁은 잘 먹었어",
			"눕자마자 푹 잤어",
		},
		diary: "일이 많아서 바빴지만 기분은 괜찮았다. 일하는 동안은 집중해서 했다. " +
			"퇴근하고 나니 피곤했다. 그래도 저녁은 잘 먹었고 눕자마자 푹 잤다.",
	},
	{
		said: []string{
			"주말이라 늦잠을 자고 푹 잤어",
			"오후에 자전거를 탔는데 즐거웠어",
			"기분이 좋아서 저녁까지 밖에 있었어",
			"몸이 가볍고 기운이 났어",
			"저녁은 맛있게 먹었어",
		},
		diary: "주말이라 늦잠을 자고 푹 잤다. 오후에 자전거를 탔는데 즐거웠다. " +
			"기분이 좋아서 저녁까지 밖에 있었다. 몸이 가볍고 기운이 났다. 저녁은 맛있게 먹었다.",
	},
	{
		said: []string{
			"아침에 알람을 못 듣고 늦게 일어났는데 그래도 기분은 괜찮았어",
			"회의에서 발표를 잘했다고 생각해",
			"점심은 동료들과 맛있게 먹었어",
			"밤에는 잘 잤어",
			"하루가 즐거웠어",
		},
		diary: "아침에 알람을 못 듣고 늦게 일어났다. 그래도 기분은 괜찮았다. " +
			"회의에서 발표를 잘했다고 생각한다. 점심은 동료들과 맛있게 먹었다. " +
			"하루가 즐거웠고 밤에는 잘 잤다.",
	},
	{
		said: []string{
			"비가 와서 하루가 좀 가라앉았어",
			"그래도 점심은 잘 먹었어",
			"오후에는 집중해서 일을 마쳤어",
			"몸이 가볍고 움직이기 편했어",
			"밤에는 푹 잤어",
		},
		diary: "비가 와서 하루가 좀 가라앉았다. 그래도 점심은 잘 먹었고 오후에는 집중해서 일을 마쳤다. " +
			"몸이 가볍고 움직이기 편했다. 밤에는 푹 잤다.",
	},
	{
		said: []string{
			"어제는 드라마를 보다가 새벽까지 못 잤어",
			"낮에는 계속 피곤했어",
			"점심은 잘 먹었어",
			"일은 집중해서 했어",
			"기분은 괜찮았어",
		},
		diary: "어제 드라마를 보다가 새벽까지 못 잤다. 낮에는 계속 피곤했다. " +
			"점심은 잘 먹었고 일은 집중해서 했다. 기분은 괜찮았다.",
	},
	{
		said: []string{
			"조카가 와서 같이 놀았는데 즐거웠어",
			"오랜만에 크게 웃었어",
			"저녁은 다 같이 맛있게 먹었어",
			"놀아 주고 나서도 기운이 났어",
			"밤에는 푹 잤어",
		},
		diary: "조카가 와서 같이 놀았는데 즐거웠다. 오랜만에 크게 웃었다. " +
			"저녁은 다 같이 맛있게 먹었다. 놀아 주고 나서도 기운이 났다. 밤에는 푹 잤다.",
	},
	{
		said: []string{
			"회의 자료를 두고 와서 좀 속상했어",
			"그래도 내 잘못은 아니라고 생각했어",
			"오후에는 집중해서 일했어",
			"점심은 잘 먹었어",
			"밤에는 잘 잤어",
		},
		diary: "회의 자료를 두고 와서 좀 속상했다. 그래도 내 잘못은 아니라고 생각했다. " +
			"오후에는 집중해서 일했다. 점심은 잘 먹었고 밤에는 잘 잤다.",
	},
	{
		said: []string{
			"오늘은 회의가 이어져서 정신이 없었어",
			"머리가 복잡해서 집중이 안 됐어",
			"저녁은 잘 먹었어",
			"밤에는 푹 잤어",
			"그래도 기분은 괜찮았어",
			"몸이 가볍고 개운했어",
		},
		diary: "회의가 이어져서 정신이 없었다. 머리가 복잡해서 집중이 안 됐다. " +
			"그래도 기분은 괜찮았고 몸이 가볍고 개운했다. 저녁은 잘 먹었고 밤에는 푹 잤다.",
	},
	{
		said: []string{
			"친구가 놀러 와서 같이 요리를 했어",
			"만든 파스타를 맛있게 먹었어",
			"이야기하는 게 즐거웠어",
			"기분이 좋았어",
			"몸이 가볍고 편한 하루였어",
			"밤에는 푹 잤어",
		},
		diary: "친구가 놀러 와서 같이 요리를 했다. 만든 파스타를 맛있게 먹었다. " +
			"이야기하는 게 즐거웠고 기분이 좋았다. 몸이 가볍고 편한 하루였다. 밤에는 푹 잤다.",
	},
	{
		said: []string{
			"오늘은 일찍 퇴근해서 산책을 했어",
			"산책하고 나니 기운이 났어",
			"집중해서 책도 조금 읽었어",
			"저녁은 잘 먹었어",
			"잠은 푹 잤어",
			"기분도 괜찮았어",
		},
		diary: "일찍 퇴근해서 산책을 했다. 산책하고 나니 기운이 났다. 집중해서 책도 조금 읽었다. " +
			"저녁은 잘 먹었고 잠도 푹 잤다. 기분도 괜찮았다.",
	},
	{
		said: []string{
			"어젯밤에 두 번 깼어",
			"그래서 아침부터 피곤했어",
			"점심은 잘 먹었어",
			"회의 자료는 집중해서 만들었어",
			"기분은 괜찮았어",
		},
		diary: "어젯밤에 두 번 깼다. 그래서 아침부터 피곤했다. " +
			"점심은 잘 먹었고 회의 자료는 집중해서 만들었다. 기분은 괜찮았다.",
	},
	{
		said: []string{
			"요즘 잠이 안 와서 새벽에 한참 누워 있었어",
			"하루 종일 기분이 가라앉았어",
			"저녁은 잘 먹었어",
			"일은 집중해서 했어",
			"저녁에 본 영화는 재밌었어",
		},
		diary: "요즘 잠이 안 와서 새벽에 한참 누워 있었다. 하루 종일 기분이 가라앉았다. " +
			"저녁은 잘 먹었고 일은 집중해서 했다. 저녁에 본 영화는 재밌었다.",
	},
	{
		said: []string{
			"어제도 잠을 설쳤어",
			"아침부터 기운이 없었어",
			"이유도 없이 우울했어",
			"점심은 잘 먹었어",
			"일은 집중해서 했어",
		},
		diary: "어제도 잠을 설쳤다. 아침부터 기운이 없었다. 이유도 없이 우울했다. " +
			"점심은 잘 먹었고 일은 집중해서 했다.",
	},
	{
		said: []string{
			"새벽까지 뒤척였어",
			"회의 중에 딴생각만 했어",
			"기분이 계속 가라앉아 있어",
			"저녁은 잘 먹었어",
			"친구랑 통화한 건 재밌었어",
		},
		diary: "새벽까지 뒤척였다. 회의 중에 딴생각만 했다. 기분이 계속 가라앉아 있다. " +
			"저녁은 잘 먹었다. 친구와 통화한 건 재밌었다.",
	},
	{
		said: []string{
			"어젯밤에도 세 번 깼어",
			"하루 종일 피곤했어",
			"오후에는 우울했어",
			"보고서에서 실수한 게 다 내 탓 같았어",
			"점심은 잘 먹었어",
		},
		diary: "어젯밤에도 세 번 깼다. 하루 종일 피곤했다. 오후에는 우울했다. " +
			"보고서에서 실수한 게 다 내 탓 같았다. 점심은 잘 먹었다.",
	},
	{
		said: []string{
			"잠을 못 자서 아침에 일어나기 힘들었어",
			"하루 종일 지쳤어",
			"입맛이 없어서 점심을 건너뛰었어",
			"뭘 해도 재미가 없었어",
			"회의 때는 집중해서 들었어",
		},
		diary: "잠을 못 자서 아침에 일어나기 힘들었다. 하루 종일 지쳤다. " +
			"입맛이 없어서 점심을 건너뛰었다. 뭘 해도 재미가 없었다. 회의 때는 집중해서 들었다.",
	},
	{
		said: []string{
			"오늘도 잠을 설쳤어",
			"오전에는 집중이 안 됐어",
			"이유도 없이 우울했어",
			"점심은 잘 먹었어",
			"저녁에 본 드라마는 재밌었어",
		},
		diary: "오늘도 잠을 설쳤다. 오전에는 집중이 안 됐다. 이유도 없이 우울했다. " +
			"점심은 잘 먹었다. 저녁에 본 드라마는 재밌었다.",
	},
	{
		said: []string{
			"밤에 두 번이나 깼어",
			"아침부터 기운이 없었어",
			"기분이 계속 우울해",
			"작은 실수도 자책이 됐어",
			"점심은 잘 먹었어",
		},
		diary: "밤에 두 번이나 깼다. 아침부터 기운이 없었다. 기분이 계속 우울하다. " +
			"작은 실수도 자책이 됐다. 점심은 잘 먹었다.",
	},
	{
		said: []string{
			"어젯밤에는 푹 잤어",
			"그래도 뭘 해도 재미가 없어",
			"책을 읽어도 집중이 안 돼",
			"아침에 몸이 무거워서 한참 앉아 있었어",
			"점심은 잘 먹었어",
		},
		diary: "어젯밤에는 푹 잤다. 그래도 뭘 해도 재미가 없다. 책을 읽어도 집중이 안 된다. " +
			"아침에 몸이 무거워서 한참 앉아 있었다. 점심은 잘 먹었다.",
	},
	{
		said: []string{
			"오늘은 하루 종일 우울했어",
			"몸이 피곤해서 일찍 누웠어",
			"전화를 못 받은 것도 내 탓 같았어",
			"저녁은 잘 먹었어",
			"잠은 푹 잤어",
		},
		diary: "하루 종일 우울했다. 몸이 피곤해서 일찍 누웠다. " +
			"전화를 못 받은 것도 내 탓 같았다. 저녁은 잘 먹었고 잠은 푹 잤다.",
	},
	{
		said: []string{
			"좋아하던 게임도 재미없었어",
			"입맛이 없어서 저녁을 굶었어",
			"다 내 탓인 것 같았어",
			"잠은 푹 잤어",
			"일은 집중해서 했어",
		},
		diary: "좋아하던 게임도 재미없었다. 입맛이 없어서 저녁을 굶었다. " +
			"다 내 탓인 것 같았다. 일은 집중해서 했고 잠은 푹 잤다.",
	},
	{
		said: []string{
			"어젯밤에 잠을 못 잤어",
			"아침부터 기분이 가라앉았어",
			"일하는 동안 집중이 안 됐어",
			"점심은 잘 먹었어",
			"저녁에 본 예능은 재밌었어",
		},
		diary: "어젯밤에 잠을 못 잤다. 아침부터 기분이 가라앉았다. 일하는 동안 집중이 안 됐다. " +
			"점심은 잘 먹었다. 저녁에 본 예능은 재밌었다.",
	},
	{
		said: []string{
			"아침에 몸이 무거워서 겨우 일어났어",
			"하루 종일 피곤했어",
			"재미있던 일도 흥미가 없어",
			"잠은 잘 잤어",
			"점심은 잘 먹었어",
		},
		diary: "아침에 몸이 무거워서 겨우 일어났다. 하루 종일 피곤했다. " +
			"재미있던 일도 흥미가 없다. 잠은 잘 잤고 점심은 잘 먹었다.",
	},
	{
		said: []string{
			"어젯밤에도 잠을 설쳤어",
			"하루 내내 우울했어",
			"작은 일도 다 내 탓 같아",
			"뭘 해도 재미가 없어",
			"점심은 잘 먹었어",
		},
		diary: "어젯밤에도 잠을 설쳤다. 하루 내내 우울했다. 작은 일도 다 내 탓 같다. " +
			"뭘 해도 재미가 없다. 점심은 잘 먹었다.",
	},
}

// seedDayAt은 오늘로부터 offset일 앞의 이야기다. 이야기의 마지막 날이 오늘이고 하루씩 거슬러 짝지운다.
//
// 오늘을 어디에 두는지가 중요하다. 흐름의 끝(신호가 잦아진 날들)이 오늘에 닿아 있어야
// 며칠만 심어도 최근 창에 그 날들이 들어온다. 이야기보다 긴 날을 심으면 가장 앞의 평범한 하루를 되풀이한다.
// 평소를 잡는 기간이 길어질 뿐 흐름은 그대로다.
func seedDayAt(offset int) seedDay {
	i := len(seedScript) - 1 - offset
	if i < 0 {
		i = 0
	}
	return seedScript[i]
}

// seedUsage는 seed 명령의 쓰임새다.
const seedUsage = `usage:
  server seed demo --email=EMAIL --password=PASSWORD [--days=N] [--name=NAME]

  APP_ENV=prod에서는 돌지 않는다. 시연과 브라우저 흐름 테스트가 볼 기록을 만드는 명령이다.
`

// seedDemo는 시연용 계정 하나와 그 계정의 지난 며칠치 기록을 만든다.
//
// 대화와 발화를 서버가 쓰는 길 그대로 저장한 다음, 실제 추출 서비스를 불러 신호를 뽑는다.
// 신호 행을 직접 써 넣지 않는 까닭: 화면에 보이는 근거와 일수가 심을 때 적어 둔 값이 아니라
// 살아 있는 추출 경로가 만든 값이어야, 시연에서 보이는 것이 실제로 도는 것과 같다.
// 작업 큐는 거치지 않는다. 명령이 끝났을 때 기록이 다 준비되어 있어야 브라우저 흐름 테스트가 기댈 수 있다.
func seedDemo(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("seed demo", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var (
		email    = flags.String("email", "", "시연 계정의 이메일")
		password = flags.String("password", "", "시연 계정의 비밀번호")
		name     = flags.String("name", "", "부를 이름 (비워 둘 수 있다)")
		days     = flags.Int("days", defaultSeedDays, "오늘까지 며칠치를 심을지")
	)
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	switch {
	case flags.NArg() != 0 || *email == "" || *password == "":
		_, _ = fmt.Fprint(stderr, seedUsage)
		return exitUsage
	case *days < 1 || *days > maxSeedDays:
		_, _ = fmt.Fprintf(stderr, "--days must be between 1 and %d\n", maxSeedDays)
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitUsage
	}
	if cfg.Env.IsProd() {
		// 이 명령은 비밀번호를 인자로 받고 지어낸 기록을 심는다. 운영 데이터베이스에 둘 일이 아니다.
		_, _ = fmt.Fprintln(stderr, "seed is refused when APP_ENV=prod")
		return exitUsage
	}

	// 진행 로그는 stderr로 보낸다. stdout에는 만든 계정의 ID만 적어서 다른 명령에 넘길 수 있게 한다.
	logger := logging.New(stderr, cfg.LogLevel).With(slog.String("app", app.NameServer))
	ctx, cancel := context.WithTimeout(ctx, seedTimeout)
	defer cancel()

	deps, err := app.New(ctx, cfg, logger, app.NameServer)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitFailure
	}
	defer deps.Close()

	userID, err := seedAccount(ctx, deps, *email, *password, *name)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitFailure
	}
	if err := seedRecords(ctx, deps, userID, *days); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitFailure
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "demo account seeded",
		slog.String("user_id", userID.String()), slog.Int("days", *days))
	_, _ = fmt.Fprintln(stdout, userID.String())
	return exitOK
}

// seedAccount는 가입 경로와 같은 길로 계정을 만들고 시연 계정으로 표시한다.
func seedAccount(ctx context.Context, deps *app.Deps, email, password, name string) (uuid.UUID, error) {
	service, err := deps.NewAuthService(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	consents := make([]auth.ConsentGrant, 0)
	for _, c := range auth.CurrentConsents() {
		consents = append(consents, auth.ConsentGrant{Kind: c.Kind, Version: c.Version})
	}
	result, err := service.Signup(ctx, auth.SignupInput{
		Email: email, Password: password, DisplayName: name, Consents: consents,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("seed: create the demo account: %w", err)
	}

	// 시연 계정 표시는 sqlc 쿼리로 두지 않는다. 운영 코드가 쓸 일이 없는 구문을 쿼리 묶음에 넣으면
	// 운영에서도 부를 수 있는 길이 생긴다. 이 명령은 운영에서 아예 돌지 않는다.
	if _, err := deps.Pool.Exec(ctx, `UPDATE users SET is_demo = true WHERE id = $1`, result.User.ID); err != nil {
		return uuid.Nil, fmt.Errorf("seed: mark the account as a demo account: %w", err)
	}
	return result.User.ID, nil
}

// seedRecords는 오늘부터 거슬러 days일 동안 하루에 대화 하나를 심고, 그 대화에서 신호를 뽑는다.
func seedRecords(ctx context.Context, deps *app.Deps, userID uuid.UUID, days int) error {
	models, err := deps.NewModels(ctx)
	if err != nil {
		return err
	}
	extractor, err := deps.NewAnalysisService(models.Analysis)
	if err != nil {
		return err
	}

	loc, err := time.LoadLocation(auth.DefaultTimezone)
	if err != nil {
		return fmt.Errorf("seed: load the default timezone: %w", err)
	}
	today := recorddate.Of(deps.Clock.Now(), loc)
	if today.IsZero() {
		return errors.New("seed: cannot resolve today's record date")
	}

	sealer, err := deps.Sealers.For(ctx, userID)
	if err != nil {
		return fmt.Errorf("seed: open the account key: %w", err)
	}

	for offset := days - 1; offset >= 0; offset-- {
		date := today.AddDays(-offset)
		if date.IsZero() {
			return errors.New("seed: the first day is outside the supported date range")
		}
		// 하루의 저녁 여덟 시로 둔다. 기록 날짜의 경계에서 흔들리지 않는 시각이다.
		start, _ := date.Bounds(loc)
		at := start.Add(20 * time.Hour)

		conversation, err := seedConversation(ctx, deps, sealer, userID, date, at, seedDayAt(offset))
		if err != nil {
			return err
		}
		if _, err := extractor.Extract(ctx, analysis.Target{
			UserID: userID, ConversationID: conversation.ID,
		}); err != nil {
			return fmt.Errorf("seed: extract signals for %s: %w", date, err)
		}
	}
	return nil
}

// seedConversation은 하루의 대화 하나를 열고, 말을 잠가 저장하고, 끝낸다.
// 작업은 등록하지 않는다. 신호는 부르는 쪽이 곧바로 뽑고, 일기 초안은 여기서 함께 써 둔다.
func seedConversation(
	ctx context.Context, deps *app.Deps, sealer *crypto.Sealer,
	userID uuid.UUID, date recorddate.Date, at time.Time, day seedDay,
) (db.Conversation, error) {
	said := day.said
	ids, err := newIDs(4 + 2*len(said))
	if err != nil {
		return db.Conversation{}, err
	}
	conversation, err := deps.Store.OpenConversation(ctx, store.NewConversation{
		ID: ids[0], NewDayID: ids[1], UserID: userID,
		RecordDate: date, StartedMode: store.ModeChat, Now: at,
	})
	if err != nil {
		return db.Conversation{}, fmt.Errorf("seed: open a conversation on %s: %w", date, err)
	}

	turns := make([]seedTurn, 0, 1+2*len(said))
	turns = append(turns, seedTurn{speaker: store.SpeakerAI, origin: store.OriginFixed, text: "오늘 하루는 어땠어요?"})
	for _, text := range said {
		turns = append(turns,
			seedTurn{speaker: store.SpeakerUser, origin: store.OriginUser, text: text},
			seedTurn{speaker: store.SpeakerAI, origin: store.OriginModel, text: "그랬군요. 더 이야기해 주세요."},
		)
	}
	for i, turn := range turns {
		id := ids[2+i]
		text, err := sealer.SealString(turn.text, sealing.UtteranceText(id))
		if err != nil {
			return db.Conversation{}, fmt.Errorf("seed: seal an utterance: %w", err)
		}
		if _, err := deps.Store.AppendUtterance(ctx, store.NewUtterance{
			ID: id, ConversationID: conversation.ID, UserID: userID,
			Speaker: turn.speaker, Modality: store.ModeChat, Origin: turn.origin,
			TextEnc: text, Now: at.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			return db.Conversation{}, fmt.Errorf("seed: append an utterance: %w", err)
		}
	}

	if _, err := deps.Store.Queries().EndConversation(ctx, db.EndConversationParams{
		Now: at.Add(time.Hour), EndReason: store.EndReasonUser, ProcessingStatus: store.ProcessingNone,
		ID: conversation.ID, UserID: userID,
	}); err != nil {
		return db.Conversation{}, fmt.Errorf("seed: end the conversation on %s: %w", date, err)
	}

	if err := seedDiary(ctx, deps, sealer, userID, conversation.DayID, ids[len(ids)-1], day.diary, at); err != nil {
		return db.Conversation{}, err
	}
	return conversation, nil
}

// seedDiary는 그날의 일기 초안을 써 둔다. 글은 이야기에 함께 적어 둔 단락이다.
//
// 초안 작업을 돌리지 않는 까닭은 이 명령이 끝났을 때 기록이 다 준비되어 있어야 하기 때문이다.
// 발화를 이어 붙여 만들지도 않는다. 이어 붙인 글은 사람이 쓴 일기로 읽히지 않는데,
// 달력에서 지난 하루를 열면 나오는 것이 바로 이 글이다.
func seedDiary(
	ctx context.Context, deps *app.Deps, sealer *crypto.Sealer,
	userID, dayID, diaryID uuid.UUID, text string, at time.Time,
) error {
	return deps.Store.InTx(ctx, func(q *db.Queries) error {
		_, err := store.SaveDiaryDraft(ctx, q, store.DiaryDraftWrite{
			NewID: diaryID, DayID: dayID, UserID: userID,
			Seal: func(rowID uuid.UUID) ([]byte, error) {
				return sealer.SealString(text, sealing.DiaryDraft(rowID))
			},
			Now: at.Add(time.Hour),
		})
		if err != nil {
			return fmt.Errorf("seed: save a diary draft: %w", err)
		}
		return nil
	})
}

// seedTurn은 심는 말 하나다.
type seedTurn struct {
	speaker string
	origin  string
	text    string
}

// newIDs는 필요한 만큼의 ID를 미리 만든다. 암호문의 AAD가 행 ID에 묶여 있어서 저장 전에 정해져야 한다.
func newIDs(n int) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, 0, n)
	for range n {
		id, err := store.NewID()
		if err != nil {
			return nil, fmt.Errorf("seed: new id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}
