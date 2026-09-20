import { Navigate, Outlet, useLocation } from 'react-router';

import { returnPathFrom, returnToState } from '@/auth/returnTo';
import { useMe } from '@/auth/session';
import { Button } from '@/components/ui/button';
import { Waiting } from '@/components/Waiting';
import { describeError } from '@/content/errorMessages';

interface SessionCheckFailedProps {
  error: unknown;
  retrying: boolean;
  onRetry: () => void;
}

function SessionCheckFailed({ error, retrying, onRetry }: SessionCheckFailedProps) {
  return (
    <div className="flex flex-1 flex-col justify-center gap-6">
      <div className="flex flex-col gap-3" role="alert">
        <h1 className="text-2xl leading-snug font-semibold">화면을 불러오지 못했어요</h1>
        <p className="text-muted-foreground">{describeError(error).message}</p>
      </div>
      <Button onClick={onRetry} disabled={retrying}>
        다시 시도하기
      </Button>
    </div>
  );
}

/**
 * 로그인한 사람만 들어가는 경로를 감싼다.
 * 로그인하지 않았으면 로그인 화면으로 보내면서 가려던 주소를 함께 넘긴다. 로그인하면 그 주소로 돌아온다.
 */
export function RequireAuth() {
  const me = useMe();
  const location = useLocation();

  // 답을 이미 알고 있으면 그것부터 믿는다. 뒤에서 다시 확인하다가 연결이 잠깐 끊겼다고 쓰던 사람을 내보내지 않는다.
  if (me.data) return <Outlet />;
  if (me.data === null) return <Navigate to="/login" replace state={returnToState(location)} />;
  if (me.isPending) return <Waiting />;

  // 확인에 실패한 것은 "로그인하지 않았다"와 다르다. 로그인 화면으로 보내면 멀쩡한 세션을 두고 다시 로그인하게 된다.
  return (
    <SessionCheckFailed
      error={me.error}
      retrying={me.isFetching}
      onRetry={() => void me.refetch()}
    />
  );
}

/** 로그인과 가입 화면을 감싼다. 이미 로그인한 사람은 가려던 곳이나 처음 화면으로 보낸다. */
export function GuestOnly() {
  const me = useMe();
  const location = useLocation();

  // 로그인이나 가입이 막 성공했을 때도 이 길로 나간다. 화면 쪽에서 따로 이동시키지 않는다.
  if (me.data) return <Navigate to={returnPathFrom(location.state)} replace />;
  // 기다리는 화면은 첫 답이 오기 전에만 보여 준다. 첫 확인이 실패한 뒤에는 다시 확인할 때마다(앱으로 돌아옴,
  // 연결이 돌아옴, 로그인 직후) 조회가 "기다리는 중"으로 되돌아간다. 그때마다 화면을 바꾸면 폼이 통째로 내려갔다
  // 올라오면서 쓰던 이메일, 비밀번호, 동의가 말없이 지워진다.
  if (me.isPending && me.errorUpdateCount === 0) return <Waiting />;
  // 확인에 실패했어도 로그인 화면은 보여 준다. 연결이 돌아오면 여기서 바로 로그인할 수 있다.
  return <Outlet />;
}
