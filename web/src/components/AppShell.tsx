import {
  BookOpenIcon,
  HouseIcon,
  LeafIcon,
  LifeBuoyIcon,
  LogOutIcon,
  MicIcon,
  SproutIcon,
} from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { Link, NavLink, Outlet, useLocation, useMatches } from 'react-router';

import { useLogout, useMe } from '@/auth/session';
import { Button } from '@/components/ui/button';
import { describeError, OFFLINE_MESSAGE } from '@/content/errorMessages';
import { useOnline } from '@/lib/useOnline';
import { cn } from '@/lib/utils';

export interface ShellHandle {
  immersive?: boolean;
  wide?: boolean;
  full?: boolean;
}

function isShellHandle(value: unknown): value is ShellHandle {
  return typeof value === 'object' && value !== null;
}

const menu = [
  { to: '/', label: '오늘', icon: HouseIcon },
  { to: '/talk', label: '이야기', icon: MicIcon },
  { to: '/diary', label: '일기장', icon: BookOpenIcon },
  { to: '/trend', label: '변화 추세', icon: SproutIcon },
];

export function AppShell() {
  const me = useMe();
  const logout = useLogout();
  const online = useOnline();
  const [logoutError, setLogoutError] = useState<string | null>(null);
  const { pathname } = useLocation();
  const mainRef = useRef<HTMLElement>(null);
  const pageRef = useRef<HTMLDivElement>(null);
  const previousPathname = useRef(pathname);
  const handles = useMatches()
    .map((match) => match.handle)
    .filter(isShellHandle);
  const immersive = handles.some((handle) => handle.immersive === true);
  const full = handles.some((handle) => handle.full === true);
  const signedIn = Boolean(me.data);
  const guestForm = pathname === '/login' || pathname === '/signup';

  useEffect(() => {
    if (previousPathname.current === pathname) return;
    previousPathname.current = pathname;
    mainRef.current?.focus({ preventScroll: true });
    document.scrollingElement?.scrollTo?.({ top: 0, behavior: 'instant' });
  }, [pathname]);

  // Animate the surface without remounting route guards, forms or live connections.
  useEffect(() => {
    if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return;
    const animation = pageRef.current?.animate?.(
      [
        { opacity: 0, transform: 'translateY(10px)' },
        { opacity: 1, transform: 'translateY(0)' },
      ],
      { duration: 400, easing: 'cubic-bezier(.2,.75,.25,1)' },
    );
    return () => animation?.cancel();
  }, [pathname]);

  function handleLogout() {
    setLogoutError(null);
    logout.mutate(undefined, {
      onError: (error) => setLogoutError(describeError(error).message),
    });
  }

  return (
    <div
      className={cn(
        'app-frame',
        signedIn && 'app-frame--member',
        immersive && 'app-frame--immersive',
      )}
    >
      <a href="#main" className="skip-link">
        본문으로 건너뛰기
      </a>
      {!immersive && (
        <>
          <header className="app-header">
            <Link to="/" aria-label="내일 처음 화면" className="brand">
              <span className="brand-mark" aria-hidden="true">
                <span />
              </span>
              <span>내일</span>
            </Link>
            <p className="app-header-note">오늘의 나를 위한 작은 기록</p>
            <div className="app-header-actions">
              <NavLink to="/help" className="help-link">
                <LifeBuoyIcon aria-hidden="true" className="size-4" />
                도움이 필요할 때
              </NavLink>
              {signedIn && (
                <Button
                  variant="ghost"
                  onClick={handleLogout}
                  disabled={logout.isPending}
                  className="logout-button"
                  aria-label="로그아웃"
                >
                  <LogOutIcon aria-hidden="true" className="size-4" />
                  <span>로그아웃</span>
                </Button>
              )}
            </div>
          </header>
          {signedIn && (
            <nav aria-label="주요 메뉴" className="app-navigation">
              <p className="nav-eyebrow" aria-hidden="true">
                나의 작은 쉼터
              </p>
              <div className="nav-items">
                {menu.map(({ to, label, icon: Icon }) => (
                  <NavLink
                    key={to}
                    to={to}
                    end={to === '/'}
                    className={({ isActive }) => cn('nav-item', isActive && 'nav-item--active')}
                  >
                    <Icon aria-hidden="true" strokeWidth={1.7} className="size-5" />
                    <span>{label}</span>
                    <span className="nav-active-dot" aria-hidden="true" />
                  </NavLink>
                ))}
              </div>
              <div className="nav-note" aria-hidden="true">
                <LeafIcon className="size-6" strokeWidth={1.3} />
                <p>
                  조금씩 쌓이는 오늘이
                  <br />더 나은 내일이 되도록.
                </p>
                <span>말이 쌓여 내가 돼요.</span>
              </div>
            </nav>
          )}
        </>
      )}
      <main
        id="main"
        ref={mainRef}
        tabIndex={-1}
        className={cn(
          'app-main',
          guestForm && 'app-main--guest',
          full && 'app-main--full',
          immersive && 'app-main--immersive',
        )}
      >
        {!immersive && logoutError !== null && (
          <p role="alert" className="mb-4 text-sm text-destructive">
            {logoutError}
          </p>
        )}
        {!immersive && !online && (
          <p role="status" className="mb-4 rounded-xl bg-muted px-4 py-3 text-sm">
            {OFFLINE_MESSAGE}
          </p>
        )}
        <div ref={pageRef} className={cn('page-entry', immersive && 'page-entry--immersive')}>
          <Outlet />
        </div>
      </main>
    </div>
  );
}
