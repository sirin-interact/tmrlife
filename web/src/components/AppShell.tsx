import { LifeBuoyIcon } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { Link, NavLink, Outlet, useLocation, useMatches } from 'react-router';

import { useLogout, useMe } from '@/auth/session';
import { Button } from '@/components/ui/button';
import { describeError, OFFLINE_MESSAGE } from '@/content/errorMessages';
import { useOnline } from '@/lib/useOnline';
import { cn } from '@/lib/utils';

/** 경로 정의의 handle에 적어 두면 이 틀이 읽는다. */
export interface ShellHandle {
  /** 대화처럼 화면 전체를 쓰는 경로. 메뉴 줄을 접고, 화면 높이에 맞춰 안에서만 스크롤한다. */
  immersive?: boolean;
  /** 글을 읽고 쓰는 경로. 태블릿에서는 폰 너비보다 넓게 쓴다. */
  wide?: boolean;
  /**
   * 표와 그림을 여러 개 나란히 놓고 보는 경로. 큰 화면에서는 폭을 더 넓게 쓴다.
   *
   * 사용자 화면은 한 손으로 읽는 글이라 좁은 폭이 옳다. 이 단계는 그 규칙을 따를 까닭이 없는 화면에만 준다.
   */
  full?: boolean;
}

function isShellHandle(value: unknown): value is ShellHandle {
  return typeof value === 'object' && value !== null;
}

const navLinkClass = ({ isActive }: { isActive: boolean }) =>
  cn(
    'inline-flex min-h-touch items-center rounded-lg px-3 font-medium text-muted-foreground hover:text-foreground',
    isActive &&
      'font-semibold text-foreground underline decoration-primary decoration-2 underline-offset-8',
  );

/** 모든 화면이 함께 쓰는 틀. 폰 너비의 한 단 구성이고, 태블릿에서는 가운데에 놓인다. */
export function AppShell() {
  const me = useMe();
  const logout = useLogout();
  const online = useOnline();
  const [logoutError, setLogoutError] = useState<string | null>(null);
  const { pathname } = useLocation();
  const mainRef = useRef<HTMLElement>(null);
  const previousPathname = useRef(pathname);

  const handles = useMatches()
    .map((match) => match.handle)
    .filter(isShellHandle);
  const immersive = handles.some((handle) => handle.immersive === true);
  const wide = handles.some((handle) => handle.wide === true);
  const full = handles.some((handle) => handle.full === true);
  const column = cn(
    'mx-auto w-full max-w-content px-gutter',
    wide && 'md:max-w-2xl',
    full && 'lg:max-w-5xl xl:max-w-6xl',
  );

  // 화면이 바뀌면 초점을 본문으로 옮긴다. 로그인 뒤처럼 누르던 버튼이 사라지는 이동에서는
  // 초점이 갈 곳을 잃고, 화면 낭독기 사용자는 화면이 바뀐 줄 모르게 된다.
  useEffect(() => {
    if (previousPathname.current === pathname) return;
    previousPathname.current = pathname;
    mainRef.current?.focus();
  }, [pathname]);

  // 로그아웃 버튼은 로그아웃이 끝나는 순간 사라진다. 뒷일이 버튼과 함께 사라지지 않도록 늘 떠 있는 이 틀이 맡는다.
  function handleLogout() {
    setLogoutError(null);
    logout.mutate(undefined, {
      // 성공했을 때 로그인 화면으로 옮기는 일은 useLogout이 세션을 비우는 것과 한 박자에 한다.
      // 로그아웃에 실패하면 세션이 살아 있다. 로그인 상태를 그대로 두고 알리기만 한다.
      onError: (error) => setLogoutError(describeError(error).message),
    });
  }

  return (
    <div className={cn('relative flex flex-col', immersive ? 'h-dvh' : 'min-h-dvh')}>
      {/* 새벽 하늘 느낌의 은은한 바탕. 장식이라 화면 낭독기에는 알리지 않는다. */}
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-x-0 top-0 -z-10 h-72 bg-linear-to-b from-dawn-from to-dawn-to"
      />

      <a
        href="#main"
        className="sr-only focus-visible:not-sr-only focus-visible:absolute focus-visible:top-3 focus-visible:left-3 focus-visible:z-10 focus-visible:rounded-lg focus-visible:bg-card focus-visible:px-4 focus-visible:py-3"
      >
        본문으로 건너뛰기
      </a>

      <header className={cn(column, 'pt-[max(1rem,env(safe-area-inset-top))]')}>
        <div className="flex items-center justify-between gap-x-4">
          <Link
            to="/"
            aria-label="내일 처음 화면"
            className="inline-flex min-h-touch items-center rounded-lg text-xl font-bold tracking-tight"
          >
            내일
          </Link>
          {/* 힘든 순간은 어느 화면에서든 올 수 있다. 로그인하지 않았어도, 대화 중이어도 이 링크는 늘 같은 자리에 있다. */}
          <NavLink
            to="/help"
            className={({ isActive }) =>
              cn(
                '-mr-2 inline-flex min-h-touch items-center gap-1.5 rounded-lg px-2 text-sm font-medium text-muted-foreground hover:text-foreground',
                isActive && 'font-semibold text-foreground',
              )
            }
          >
            <LifeBuoyIcon aria-hidden="true" className="size-4" />
            도움이 필요할 때
          </NavLink>
        </div>

        {me.data && !immersive && (
          <div className="-mx-3 flex flex-wrap items-center justify-between">
            <nav aria-label="주요 메뉴" className="flex items-center">
              <NavLink to="/" end className={navLinkClass}>
                오늘
              </NavLink>
              <NavLink to="/diary" className={navLinkClass}>
                일기장
              </NavLink>
              {/* 이 앱이 가장 보여 주고 싶은 화면이다. 첫 화면의 카드를 거치지 않고도 갈 수 있게 둔다. */}
              <NavLink to="/trend" className={navLinkClass}>
                변화 추세
              </NavLink>
            </nav>
            <Button
              variant="ghost"
              onClick={handleLogout}
              disabled={logout.isPending}
              className="px-3 font-medium text-muted-foreground"
            >
              로그아웃
            </Button>
          </div>
        )}
        {logoutError !== null && (
          <p role="alert" className="text-sm text-destructive">
            {logoutError}
          </p>
        )}
        {!online && (
          <p role="status" className="mt-2 rounded-lg bg-muted px-4 py-2 text-sm">
            {OFFLINE_MESSAGE}
          </p>
        )}
      </header>

      <main
        id="main"
        ref={mainRef}
        tabIndex={-1}
        // 본문 영역은 누르는 대상이 아니라 초점이 머무는 자리일 뿐이다. 화면 전체에 초점 테두리가 둘리지 않게 한다.
        className={cn(
          column,
          'flex flex-1 flex-col outline-none',
          immersive ? 'min-h-0 pt-2' : 'pt-6 pb-[max(2.5rem,env(safe-area-inset-bottom))]',
        )}
      >
        <Outlet />
      </main>
    </div>
  );
}
