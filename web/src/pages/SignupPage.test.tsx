import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';

import { MEDICAL_NOTICE_TEXT } from '@/components/MedicalNotice';
import { CONSENT_COPY } from '@/content/consentCopy';
import {
  json,
  mockApi,
  networkFailure,
  problem,
  signedOut,
  testMe,
  testRequirements,
  testUser,
} from '@/test/mockApi';
import { renderRoute } from '@/test/renderRoute';

const PASSWORD = 'correct horse battery';
const requirementsOk = () => json(200, testRequirements);

const consentGroup = () => screen.findByRole('group', { name: /필수 동의/ });
const consentBox = (group: HTMLElement, title: string) =>
  within(group).getByRole('checkbox', { name: new RegExp(title) });

async function fillValidForm(user = userEvent.setup()) {
  const group = await consentGroup();
  await user.type(screen.getByLabelText('이메일'), testUser.email);
  await user.type(screen.getByLabelText('비밀번호'), PASSWORD);
  await user.click(within(group).getByRole('checkbox', { name: '아래 내용에 모두 동의해요' }));
  return user;
}

const submit = (user: ReturnType<typeof userEvent.setup>) =>
  user.click(screen.getByRole('button', { name: '가입하기' }));

describe('SignupPage: 동의', () => {
  it('서버가 알려준 동의 네 가지를 따로따로, 쉬운 설명과 함께 보여 준다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut, 'GET /api/v1/auth/requirements': requirementsOk });
    renderRoute('/signup');

    const group = await consentGroup();

    for (const { kind } of testRequirements.consents) {
      const copy = CONSENT_COPY[kind];
      const checkbox = consentBox(group, copy.title);
      expect(checkbox).not.toBeChecked();
      expect(checkbox).toBeRequired();
      expect(checkbox).toHaveAccessibleDescription(copy.description);
    }
    // 네 가지에 "모두 동의"를 더해 다섯 개다.
    expect(within(group).getAllByRole('checkbox')).toHaveLength(5);
    expect(consentBox(group, '민감정보')).toHaveAccessibleName(/마음과 건강/);
    expect(consentBox(group, '외부 AI 서비스')).toHaveAccessibleDescription(/해외/);
  });

  it('동의 항목은 화면에 박혀 있지 않고 서버의 응답을 따른다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': () =>
        json(200, {
          ...testRequirements,
          consents: [
            { kind: 'sensitive_data', version: CONSENT_COPY.sensitive_data.version },
            { kind: 'terms', version: CONSENT_COPY.terms.version },
          ],
        }),
    });
    renderRoute('/signup');

    const group = await consentGroup();
    const names = within(group)
      .getAllByRole('checkbox')
      .map((checkbox) => checkbox.getAttribute('id'));

    expect(names).toEqual(['consent-all', 'consent-sensitive_data', 'consent-terms']);
  });

  it('"모두 동의"는 전부 켜고 끄며, 항목을 하나 끄면 함께 꺼진다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut, 'GET /api/v1/auth/requirements': requirementsOk });
    renderRoute('/signup');
    const user = userEvent.setup();
    const group = await consentGroup();
    const all = within(group).getByRole('checkbox', { name: '아래 내용에 모두 동의해요' });
    const [, ...each] = within(group).getAllByRole('checkbox');

    await user.click(all);
    for (const checkbox of each) expect(checkbox).toBeChecked();

    await user.click(consentBox(group, '개인정보 처리방침'));
    expect(all).not.toBeChecked();
    expect(consentBox(group, '서비스 이용약관')).toBeChecked();

    await user.click(consentBox(group, '개인정보 처리방침'));
    expect(all).toBeChecked();

    await user.click(all);
    for (const checkbox of each) expect(checkbox).not.toBeChecked();
  });

  it('동의를 하나라도 빼고 제출하면 보내지 않고 알린다', async () => {
    const api = mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': requirementsOk,
    });
    renderRoute('/signup');
    const user = await fillValidForm();
    const group = await consentGroup();
    await user.click(consentBox(group, '외부 AI 서비스'));

    await submit(user);

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '필수 동의 항목을 모두 확인해 주세요.',
    );
    expect(group).toHaveAccessibleDescription('필수 동의 항목을 모두 확인해 주세요.');
    expect(consentBox(group, '외부 AI 서비스')).toBeInvalid();
    expect(consentBox(group, '서비스 이용약관')).toBeValid();
    expect(api.callsTo('POST /api/v1/auth/signup')).toHaveLength(0);
  });

  it('이 앱이 설명을 갖고 있지 않은 동의를 서버가 요구하면 가입을 받지 않는다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': () =>
        json(200, {
          ...testRequirements,
          consents: [...testRequirements.consents, { kind: 'marketing', version: '2027-01-01' }],
        }),
    });
    renderRoute('/signup');

    expect(await screen.findByRole('alert')).toHaveTextContent('화면을 새로 고친 뒤');
    expect(screen.getByRole('button', { name: '가입하기' })).toBeDisabled();
  });

  it('서버가 알려준 판이 이 앱이 가진 글의 판과 다르면, 그 동의를 받지 않고 가입도 받지 않는다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': () =>
        json(200, {
          ...testRequirements,
          consents: testRequirements.consents.map((consent) =>
            consent.kind === 'privacy' ? { ...consent, version: '2027-01-01' } : consent,
          ),
        }),
    });
    renderRoute('/signup');

    const group = await consentGroup();

    // 옛 글을 보여 주면서 새 판에 대한 동의로 기록할 수는 없다.
    expect(within(group).queryByRole('checkbox', { name: /개인정보 처리방침/ })).toBeNull();
    expect(screen.getByRole('alert')).toHaveTextContent('화면을 새로 고친 뒤');
    expect(screen.getByRole('button', { name: '가입하기' })).toBeDisabled();
  });

  it('동의 항목마다 전문을 여는 링크가 있고, 열었다 닫아도 쓰던 폼이 그대로다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut, 'GET /api/v1/auth/requirements': requirementsOk });
    renderRoute('/signup');
    const user = await fillValidForm();

    const link = screen.getByRole('link', { name: '국외 이전 안내 읽기' });
    expect(link).toHaveAttribute('href', '/legal/privacy#overseas-transfer');
    // 가운데 버튼이나 Ctrl로 열면 새 탭에서 열린다. 그 길로도 폼을 잃지 않는다.
    expect(link).toHaveAttribute('target', '_blank');
    await user.click(link);

    const sheet = await screen.findByRole('dialog', { name: '개인정보 처리방침' });
    expect(
      within(sheet).getByRole('heading', { name: '외부 AI 서비스 이용과 국외 이전' }),
    ).toHaveFocus();
    expect(within(sheet).getByText(/Google LLC/)).toBeInTheDocument();

    await user.click(within(sheet).getAllByRole('button', { name: '닫고 돌아가기' })[0]!);

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(link).toHaveFocus();
    expect(screen.getByLabelText('이메일')).toHaveValue(testUser.email);
    expect(screen.getByLabelText('비밀번호')).toHaveValue(PASSWORD);
    const group = await consentGroup();
    expect(consentBox(group, '외부 AI 서비스')).toBeChecked();
  });

  it('겹쳐 띄운 문서는 Esc로도 닫힌다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut, 'GET /api/v1/auth/requirements': requirementsOk });
    renderRoute('/signup');
    const user = userEvent.setup();
    await consentGroup();

    await user.click(screen.getByRole('link', { name: '서비스 이용약관 읽기' }));
    expect(await screen.findByRole('dialog', { name: '서비스 이용약관' })).toBeInTheDocument();
    await user.keyboard('{Escape}');

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('문서 링크를 눌러도 체크박스는 바뀌지 않고, 줄의 빈 자리를 누르면 바뀐다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut, 'GET /api/v1/auth/requirements': requirementsOk });
    renderRoute('/signup');
    const user = userEvent.setup();
    const group = await consentGroup();
    const terms = consentBox(group, '서비스 이용약관');

    await user.click(screen.getByRole('link', { name: '서비스 이용약관 읽기' }));
    await user.keyboard('{Escape}');
    expect(terms).not.toBeChecked();

    // 줄은 체크박스와 라벨 둘로만 이루어진다. 둘 사이에 눌리지 않는 틈(gap)이 없고, 라벨이 줄의 남은 너비와 48px 이상의 높이를 차지한다.
    const row = terms.closest('[data-consent-row]');
    const label = row?.querySelector('label');
    // (체크박스 컴포넌트가 폼 제출용으로 끼워 넣는 숨은 input은 자리를 차지하지 않으므로 세지 않는다.)
    const parts = Array.from(row?.children ?? []).filter((child) => child.tagName !== 'INPUT');
    expect(parts).toEqual([terms, label]);
    expect(row?.className).not.toMatch(/\bgap-/);
    expect(label?.className).toMatch(/\bmin-h-touch\b/);
    expect(label?.className).toMatch(/\bflex-1\b/);
    // 상자의 누르는 영역도 사방으로 넓혀 둔다.
    expect(terms.className).toMatch(/after:-inset-3/);

    // 글자가 아니라 라벨 자체(글자 옆의 빈 자리)를 눌러도 켜진다.
    await user.click(label!);
    expect(terms).toBeChecked();
  });

  it('가입 규칙을 받지 못하면 제출을 막고 다시 불러올 수 있게 한다', async () => {
    let online = false;
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': () => (online ? requirementsOk() : networkFailure()),
    });
    renderRoute('/signup');

    expect(await screen.findByRole('alert')).toHaveTextContent('인터넷 연결이 고르지 않아요.');
    expect(screen.getByRole('button', { name: '가입하기' })).toBeDisabled();

    online = true;
    await userEvent.click(screen.getByRole('button', { name: '다시 불러오기' }));

    expect(await consentGroup()).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '가입하기' })).toBeEnabled();
  });
});

describe('SignupPage: 비밀번호', () => {
  it('규칙의 숫자는 서버가 알려준 값이고, 입력하는 대로 채웠는지 보여 준다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': () =>
        json(200, { ...testRequirements, password: { min_length: 12, max_bytes: 128 } }),
    });
    renderRoute('/signup');
    const user = userEvent.setup();
    await consentGroup();
    const password = screen.getByLabelText('비밀번호');

    expect(password).toHaveAccessibleDescription(/12자 이상, 아직이에요\./);

    await user.type(password, 'elevenchars');
    expect(password).toHaveAccessibleDescription(/12자 이상, 아직이에요\./);

    await user.type(password, '!');
    expect(password).toHaveAccessibleDescription(/12자 이상, 채웠어요\./);
  });

  it('이메일 주소와 같은 비밀번호는 입력하는 동안에도, 제출할 때도 알려준다', async () => {
    const api = mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': requirementsOk,
    });
    renderRoute('/signup');
    const user = await fillValidForm();
    const password = screen.getByLabelText('비밀번호');
    await user.clear(password);
    await user.type(password, testUser.email.toUpperCase());

    expect(password).toHaveAccessibleDescription(/이메일 주소와 다르게, 아직이에요\./);

    await submit(user);

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '이메일 주소와 같은 비밀번호는 쓸 수 없어요.',
    );
    expect(password).toBeInvalid();
    expect(api.callsTo('POST /api/v1/auth/signup')).toHaveLength(0);
  });

  it('너무 짧은 비밀번호는 보내지 않고, 다른 칸의 오류와 함께 알려준다', async () => {
    const api = mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': requirementsOk,
    });
    renderRoute('/signup');
    const user = userEvent.setup();
    await consentGroup();
    await user.type(screen.getByLabelText('이메일'), 'not-an-email');
    await user.type(screen.getByLabelText('비밀번호'), 'short');

    await submit(user);

    const summary = await screen.findByRole('alert');
    expect(within(summary).getByText('이메일 주소를 다시 확인해 주세요.')).toBeInTheDocument();
    expect(within(summary).getByText('비밀번호는 10자 이상이어야 해요.')).toBeInTheDocument();
    expect(within(summary).getByText('필수 동의 항목을 모두 확인해 주세요.')).toBeInTheDocument();
    expect(screen.getByLabelText('이메일')).toHaveFocus();
    expect(api.callsTo('POST /api/v1/auth/signup')).toHaveLength(0);
  });

  it('비밀번호를 보이게 할 수 있다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut, 'GET /api/v1/auth/requirements': requirementsOk });
    renderRoute('/signup');
    await consentGroup();
    const password = screen.getByLabelText('비밀번호');
    expect(password).toHaveAttribute('type', 'password');
    expect(password).toHaveAttribute('autocomplete', 'new-password');

    await userEvent.click(screen.getByRole('button', { name: '비밀번호 보기' }));

    expect(password).toHaveAttribute('type', 'text');
  });
});

describe('SignupPage: 제출', () => {
  function serverWithSignup() {
    let signedUp = false;
    return mockApi({
      'GET /api/v1/me': () => (signedUp ? json(200, testMe) : problem(401, 'unauthenticated')),
      'GET /api/v1/auth/requirements': requirementsOk,
      'POST /api/v1/auth/signup': () => {
        signedUp = true;
        return json(201, { user: testUser });
      },
    });
  }

  it('가입하면 처음 화면으로 가고, 서버가 준 동의를 판까지 그대로 돌려보낸다', async () => {
    const api = serverWithSignup();
    const { router } = renderRoute('/signup');
    const user = await fillValidForm();
    await user.type(screen.getByLabelText(/부를 이름/), '  새벽  ');

    await submit(user);

    expect(await screen.findByRole('link', { name: '오늘 이야기하기' })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe('/');
    expect(api.callsTo('POST /api/v1/auth/signup')[0]?.body).toEqual({
      email: testUser.email,
      password: PASSWORD,
      display_name: '새벽',
      timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      consents: testRequirements.consents,
    });
  });

  it('부를 이름을 비워 두면 그 값을 보내지 않는다', async () => {
    const api = serverWithSignup();
    renderRoute('/signup');
    const user = await fillValidForm();

    await submit(user);

    await screen.findByRole('link', { name: '오늘 이야기하기' });
    expect(api.callsTo('POST /api/v1/auth/signup')[0]?.body).not.toHaveProperty('display_name');
  });

  it('부를 이름이 서버가 알려준 길이를 넘으면 보내지 않는다', async () => {
    const api = mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': requirementsOk,
    });
    renderRoute('/signup');
    const user = await fillValidForm();
    await user.type(screen.getByLabelText(/부를 이름/), '가'.repeat(41));

    await submit(user);

    expect(await screen.findByLabelText(/부를 이름/)).toHaveAccessibleDescription(
      /이름은 40자까지 쓸 수 있어요\./,
    );
    expect(api.callsTo('POST /api/v1/auth/signup')).toHaveLength(0);
  });

  it('요청을 보내는 동안에는 제출 버튼이 꺼져 있다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': requirementsOk,
      'POST /api/v1/auth/signup': () => new Promise<Response>(() => undefined),
    });
    renderRoute('/signup');
    const user = await fillValidForm();

    await submit(user);

    expect(await screen.findByRole('button', { name: '가입하고 있어요' })).toBeDisabled();
  });

  it('이미 가입된 이메일이면 이메일 칸에 표시하고 초점을 옮긴다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': requirementsOk,
      'POST /api/v1/auth/signup': () => problem(409, 'email_taken'),
    });
    renderRoute('/signup');
    const user = await fillValidForm();

    await submit(user);

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '이미 가입된 이메일이에요. 로그인해 주세요.',
    );
    const email = screen.getByLabelText('이메일');
    expect(email).toBeInvalid();
    expect(email).toHaveAccessibleDescription('이미 가입된 이메일이에요.');
    expect(email).toHaveFocus();
    expect(screen.getByRole('button', { name: '가입하기' })).toBeEnabled();
  });

  it('서버만 아는 이유(흔한 비밀번호)로 거절되면 비밀번호 칸에 그 이유를 적는다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': requirementsOk,
      'POST /api/v1/auth/signup': () => problem(422, 'weak_password', { reasons: ['too_common'] }),
    });
    renderRoute('/signup');
    const user = await fillValidForm();

    await submit(user);

    const password = screen.getByLabelText('비밀번호');
    await waitFor(() => expect(password).toBeInvalid());
    expect(password).toHaveAccessibleDescription(
      /많이 쓰여서 짐작하기 쉬운 비밀번호예요\. 다른 비밀번호를 정해 주세요\./,
    );
    expect(screen.getByRole('alert')).toHaveTextContent('비밀번호를 다시 정해 주세요.');
  });

  it('동의 문서의 판이 바뀌었으면 규칙을 다시 받아 오고, 이 앱에 새 판의 글이 없으면 가입을 멈춘다', async () => {
    let version = '2026-09-20';
    const api = mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': () =>
        json(200, {
          ...testRequirements,
          consents: testRequirements.consents.map((consent) =>
            consent.kind === 'terms' ? { ...consent, version } : consent,
          ),
        }),
      'POST /api/v1/auth/signup': () => {
        version = '2027-01-01';
        return problem(422, 'consent_required', { consents: { missing: [], outdated: ['terms'] } });
      },
    });
    renderRoute('/signup');
    const user = await fillValidForm();

    await submit(user);

    // 같은 안내가 폼의 요약과 동의 묶음 아래, 두 곳에 나온다.
    expect(
      await screen.findAllByText(
        '동의 내용이 새로 바뀌었어요. 내용을 확인하고 다시 동의해 주세요.',
      ),
    ).not.toHaveLength(0);
    await waitFor(() => expect(api.callsTo('GET /api/v1/auth/requirements')).toHaveLength(2));
    const group = await consentGroup();
    // 이 앱에는 새 판의 글이 없다. 옛 글로 새 판의 동의를 받지 않고, 새로 고침을 권한다.
    await waitFor(() =>
      expect(within(group).queryByRole('checkbox', { name: /서비스 이용약관/ })).toBeNull(),
    );
    expect(consentBox(group, '개인정보 처리방침')).toBeChecked();
    expect(
      screen.getByText(
        '가입에 필요한 안내를 모두 보여 드리지 못했어요. 화면을 새로 고친 뒤 다시 시도해 주세요.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '가입하기' })).toBeDisabled();
  });

  it('시도 한도에 걸리면 기다릴 시간을 알려준다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': requirementsOk,
      'POST /api/v1/auth/signup': () => problem(429, 'rate_limited', {}, { 'Retry-After': '180' }),
    });
    renderRoute('/signup');
    const user = await fillValidForm();

    await submit(user);

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '짧은 시간에 요청이 많이 몰렸어요. 3분 뒤에 다시 시도해 주세요.',
    );
  });
});

describe('SignupPage: 고지', () => {
  it('의료 서비스가 아니라는 고지를 늘 보여 준다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': () => new Promise<Response>(() => undefined),
    });
    renderRoute('/signup');

    expect(await screen.findByRole('note')).toHaveTextContent(MEDICAL_NOTICE_TEXT);
    // 규칙을 기다리는 동안에는 가입할 수 없다.
    expect(screen.getByRole('button', { name: '가입하기' })).toBeDisabled();
  });

  it('제출이 실패한 뒤에도 고지는 그대로 있다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': requirementsOk,
      'POST /api/v1/auth/signup': networkFailure,
    });
    renderRoute('/signup');
    const user = await fillValidForm();

    await submit(user);

    expect(await screen.findByRole('alert')).toHaveTextContent('인터넷 연결이 고르지 않아요.');
    expect(screen.getByRole('note')).toHaveTextContent(MEDICAL_NOTICE_TEXT);
  });
});
