import {
  ArrowDownIcon,
  ArrowRightIcon,
  AudioLinesIcon,
  BookOpenIcon,
  LeafIcon,
  MoveUpRightIcon,
  RefreshCwIcon,
  SparklesIcon,
} from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router';

import { useMe } from '@/auth/session';
import { Button } from '@/components/ui/button';
import { formatRecordDate, recordDateOf } from '@/lib/recordDate';
import { useNow } from '@/lib/useNow';

const prompts = [
  '오늘, 마음에 가장 오래 남은 순간은 무엇인가요?',
  '오늘의 나에게 한마디를 건넨다면 뭐라고 할까요?',
  '잠깐이라도 편안해졌던 순간이 있었나요?',
  '아직 말하지 못한 이야기가 있나요?',
];

/** 실제 기록은 일기장과 변화 추세에서 불러온다. 첫 화면에는 기록을 지어내지 않는다. */
export function HomePage() {
  const me = useMe();
  const now = useNow();
  const [promptIndex, setPromptIndex] = useState(0);
  const user = me.data?.user;
  const name = user?.display_name?.trim();
  const today = recordDateOf(now, user?.timezone);

  return (
    <div className="home-page">
      <title>내일</title>
      <header className="home-greeting">
        <div>
          <p className="eyebrow">나를 위한 잠깐</p>
          <h1>{name ? `${name}님, 안녕하세요` : '안녕하세요'}</h1>
          <p className="home-greeting-note">오늘도 찾아와 주셔서 고마워요.</p>
        </div>
        <time className="home-date" dateTime={today}>
          {formatRecordDate(today)}
        </time>
      </header>
      <section className="today-hero" aria-labelledby="today-title">
        <div className="today-hero-copy">
          <span className="soft-label">
            <span className="live-dot" />
            하루 5분, 나를 돌보는 습관
          </span>
          <h2 id="today-title">
            오늘의 마음에
            <br />귀 기울이는 시간.
          </h2>
          <p>
            잘한 하루도, 조금 버거웠던 하루도.
            <br />
            편하게 이야기해 주세요.
            <br className="mobile-break" /> 내일이 그 이야기를 담아둘게요.
          </p>
          <Button asChild size="lg" className="today-cta">
            <Link to="/talk">
              <AudioLinesIcon aria-hidden="true" />
              오늘 이야기하기
              <ArrowRightIcon aria-hidden="true" className="cta-arrow" />
            </Link>
          </Button>
          <span className="hero-footnote">목소리로, 또는 글로 편하게 시작해요</span>
        </div>
        <div className="hero-garden" aria-hidden="true">
          <div className="garden-orbit garden-orbit--outer" />
          <div className="garden-orbit garden-orbit--inner" />
          <span className="garden-star garden-star--one">✳</span>
          <span className="garden-star garden-star--two">+</span>
          <div className="garden-sphere">
            <span className="garden-sphere-shine" />
          </div>
          <div className="garden-shadow" />
          <div className="garden-caption">
            <span />
            있는 그대로의 나를 만나요
          </div>
          <span className="garden-coordinate">숨을 들이쉬고, 내쉬고</span>
        </div>
      </section>
      <section className="daily-prompt" aria-label="오늘을 꺼내는 작은 질문">
        <div className="prompt-icon">
          <SparklesIcon aria-hidden="true" strokeWidth={1.5} />
        </div>
        <div className="prompt-copy">
          <p>오늘을 꺼내는 작은 질문</p>
          <p key={promptIndex} className="prompt-question" aria-live="polite">
            {prompts[promptIndex]}
          </p>
        </div>
        <button
          type="button"
          className="prompt-refresh"
          aria-label="다른 질문 보기"
          onClick={() => setPromptIndex((current) => (current + 1) % prompts.length)}
        >
          <RefreshCwIcon aria-hidden="true" className="size-4" />
        </button>
      </section>
      <section aria-labelledby="record-title" className="home-records">
        <div className="section-heading">
          <h2 id="record-title">차곡차곡, 나의 이야기</h2>
          <span>
            오늘이 모여 나를 알아가요
            <ArrowDownIcon aria-hidden="true" className="size-3" />
          </span>
        </div>
        <div className="home-card-grid">
          <article className="home-feature home-feature--diary">
            <div className="feature-top">
              <span className="feature-icon">
                <BookOpenIcon aria-hidden="true" strokeWidth={1.5} />
              </span>
              <span className="eyebrow">나의 일기</span>
            </div>
            <h3>말이 글이 되는 곳</h3>
            <p>
              나눈 이야기가 한 편의 일기로.
              <br />
              그날의 마음을 천천히 펼쳐 보세요.
            </p>
            <div className="journal-decoration" aria-hidden="true">
              <span />
              <span />
              <span />
              <i>
                <LeafIcon />
              </i>
            </div>
            <Link to="/diary" className="feature-link">
              일기장 보기
              <ArrowRightIcon aria-hidden="true" className="size-4" />
            </Link>
          </article>
          <article className="home-feature home-feature--trend">
            <div className="feature-top">
              <span className="feature-icon">
                <LeafIcon aria-hidden="true" strokeWidth={1.5} />
              </span>
              <span className="eyebrow">조금씩, 천천히</span>
            </div>
            <h3>조금씩 보이는 나의 마음</h3>
            <p>
              기분과 잠, 기운이 지나온 자리.
              <br />
              기록 속에서 나만의 흐름을 발견해요.
            </p>
            <div className="trend-decoration" aria-hidden="true">
              {Array.from({ length: 28 }, (_, i) => (
                <span key={i} />
              ))}
            </div>
            <Link to="/trend" className="feature-link">
              변화 추세 보기
              <ArrowRightIcon aria-hidden="true" className="size-4" />
            </Link>
          </article>
        </div>
      </section>
      <Link to={`/signals/${today}`} className="today-signals">
        <span>
          <span className="signal-dot" />
          오늘 읽어 낸 신호 보기
        </span>
        <MoveUpRightIcon aria-hidden="true" className="size-4" />
      </Link>
      <footer className="home-footer">
        <span className="footer-flower" aria-hidden="true">
          ✳
        </span>
        매일 완벽하지 않아도 괜찮아요. 여기엔 늘 내 자리가 있어요.
      </footer>
    </div>
  );
}
