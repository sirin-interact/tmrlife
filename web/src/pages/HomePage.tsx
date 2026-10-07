import { ChevronRightIcon } from 'lucide-react';
import { Link } from 'react-router';

import { useMe } from '@/auth/session';
import { Button } from '@/components/ui/button';
import { formatRecordDate, recordDateOf } from '@/lib/recordDate';
import { useNow } from '@/lib/useNow';

/**
 * 로그인한 사람의 첫 화면. 오늘의 대화로 들어가는 자리다.
 * 기록은 일기장과 변화 추세에서 불러온다. 이 화면은 기록을 지어내지 않고 갈 곳만 알려 준다.
 */
export function HomePage() {
  const me = useMe();
  const now = useNow();
  const user = me.data?.user;
  const name = user?.display_name?.trim();
  // 달력 날짜가 아니라 기록 날짜를 보여 준다. 새벽 1시에 나눈 이야기는 서버가 전날의 기록으로 남긴다.
  const today = recordDateOf(now, user?.timezone);

  // 줄 전체가 눌리지만 링크의 이름은 짧게 둔다. 설명까지 이름에 넣으면 화면 낭독기가 한 줄을 통째로 읽는다.
  const links = [
    { to: '/diary', label: '일기장 보기', note: '이야기한 날마다 일기가 한 편씩 남아요.' },
    { to: '/trend', label: '변화 추세 보기', note: '기분, 잠, 기운을 날짜별로 봐요.' },
    {
      to: `/signals/${today}`,
      label: '오늘 읽어 낸 신호 보기',
      note: '오늘 대화에서 읽은 내용과 그 근거예요.',
    },
  ];

  return (
    <div className="home-page">
      <title>내일</title>
      <header className="home-greeting">
        <time className="home-date" dateTime={today}>
          {formatRecordDate(today)}
        </time>
        <h1>{name ? `${name}님, 안녕하세요` : '안녕하세요'}</h1>
      </header>

      <section className="home-today" aria-labelledby="today-title">
        <h2 id="today-title">오늘 하루는 어떠셨어요?</h2>
        <p>말이나 글로 이야기하면 일기로 정리해 드려요.</p>
        <Button asChild size="lg" className="home-today__action">
          <Link to="/talk">오늘 이야기하기</Link>
        </Button>
      </section>

      <ul className="home-links">
        {links.map(({ to, label, note }) => (
          <li key={to} className="home-link">
            <div>
              <Link to={to}>{label}</Link>
              <p>{note}</p>
            </div>
            <ChevronRightIcon aria-hidden="true" className="size-5" />
          </li>
        ))}
      </ul>
    </div>
  );
}
