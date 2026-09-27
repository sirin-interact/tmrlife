import { Link } from 'react-router';

import { useMe } from '@/auth/session';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardFooter, CardHeader } from '@/components/ui/card';
import { formatRecordDate, recordDateOf } from '@/lib/recordDate';
import { useNow } from '@/lib/useNow';

/** 로그인한 사람의 첫 화면. 오늘의 대화로 들어가는 자리다. */
export function HomePage() {
  const me = useMe();
  const now = useNow();

  // 이 화면은 로그인한 사람에게만 열리므로 값이 있다. 로그아웃하는 찰나에만 비어 있을 수 있다.
  const user = me.data?.user;
  const name = user?.display_name?.trim();
  // 달력 날짜가 아니라 기록 날짜를 보여 준다. 새벽 1시에 들어온 사람이 나눌 이야기는 서버가 전날의 기록으로 남긴다.
  // 여기에 내일 날짜가 떠 있으면 일기장에 남는 날짜와 이 화면이 말한 날짜가 어긋난다.
  const today = recordDateOf(now, user?.timezone);

  return (
    <div className="flex flex-col gap-8">
      <title>내일</title>
      <div className="flex flex-col gap-2">
        <p className="text-muted-foreground">
          <time dateTime={today}>{formatRecordDate(today)}</time>
        </p>
        <h1 className="text-3xl leading-snug font-semibold">
          {name ? `${name}님, 안녕하세요` : '안녕하세요'}
        </h1>
        <p className="text-muted-foreground">오늘도 찾아와 주셔서 고마워요.</p>
      </div>

      <Card>
        <CardHeader>
          <h2 className="text-xl leading-snug font-semibold">오늘 하루는 어떠셨어요?</h2>
        </CardHeader>
        <CardContent>
          <p className="text-muted-foreground">
            편하게 이야기하면 그 대화가 일기가 돼요. 기록이 쌓이면 내 마음이 어떻게 변해 왔는지
            돌아볼 수 있어요.
          </p>
        </CardContent>
        <CardFooter>
          <Button asChild size="lg" className="w-full">
            <Link to="/talk">오늘 이야기하기</Link>
          </Button>
          <Button asChild variant="ghost" className="w-full">
            <Link to="/diary">일기장 보기</Link>
          </Button>
        </CardFooter>
      </Card>

      <Card>
        <CardHeader>
          <h2 className="text-xl leading-snug font-semibold">기록이 쌓이면 보이는 것</h2>
        </CardHeader>
        <CardContent>
          <p className="text-muted-foreground">
            기분과 잠, 기운이 어떻게 지나왔는지 점으로 볼 수 있어요. 그렇게 본 까닭도 내 말로 확인할
            수 있어요.
          </p>
        </CardContent>
        <CardFooter>
          <Button asChild variant="outline" className="w-full">
            <Link to="/trend">변화 추세 보기</Link>
          </Button>
          <Button asChild variant="ghost" className="w-full">
            {/* 오늘의 근거 화면. 아직 정리 중이면 그 화면이 조용히 알려 준다. */}
            <Link to={`/signals/${today}`}>오늘 읽어 낸 신호 보기</Link>
          </Button>
        </CardFooter>
      </Card>
    </div>
  );
}
