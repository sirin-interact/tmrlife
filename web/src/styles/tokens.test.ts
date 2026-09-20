import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

const root = resolve(import.meta.dirname, '../..');
const read = (path: string) => readFileSync(resolve(root, path), 'utf8');

type Tokens = Record<string, string>;

function parseBlock(css: string): Tokens {
  const tokens: Tokens = {};
  for (const match of css.matchAll(/--([a-z-]+):\s*(#[0-9a-f]{6})\s*;/gi)) {
    const [, name, value] = match;
    if (name && value) tokens[name] = value.toLowerCase();
  }
  return tokens;
}

function loadThemes(): { light: Tokens; dark: Tokens } {
  const css = read('src/styles/tokens.css');
  const darkStart = css.indexOf('@media (prefers-color-scheme: dark)');
  expect(darkStart).toBeGreaterThan(0);

  const light = parseBlock(css.slice(0, darkStart));
  // 어두운 테마는 바뀌는 값만 다시 적으므로 밝은 테마 위에 덮어쓴다.
  return { light, dark: { ...light, ...parseBlock(css.slice(darkStart)) } };
}

function luminance(hex: string): number {
  const value = Number.parseInt(hex.slice(1), 16);
  const [r, g, b] = [(value >> 16) & 255, (value >> 8) & 255, value & 255].map((channel) => {
    const s = channel / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  }) as [number, number, number];
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}

// [글자색, 바탕색]. 본문 크기 글자는 4.5:1 이상이어야 한다.
const TEXT_PAIRS: ReadonlyArray<readonly [string, string]> = [
  ['foreground', 'background'],
  ['foreground', 'card'],
  ['foreground', 'muted'],
  ['card-foreground', 'card'],
  ['muted-foreground', 'background'],
  ['muted-foreground', 'card'],
  ['muted-foreground', 'muted'],
  ['primary-foreground', 'primary'],
  ['secondary-foreground', 'secondary'],
  ['accent-foreground', 'accent'],
  ['destructive-foreground', 'destructive'],
  ['destructive', 'card'],
  ['link', 'background'],
  ['link', 'card'],
  // 장식용 그라데이션 위에 머리말 글자가 놓인다.
  ['foreground', 'dawn-from'],
  ['muted-foreground', 'dawn-from'],
];

// 입력란 테두리와 초점 표시는 글자가 아니어도 3:1 이상이어야 한다.
const UI_PAIRS: ReadonlyArray<readonly [string, string]> = [
  ['input', 'background'],
  ['input', 'card'],
  ['ring', 'background'],
  ['ring', 'card'],
];

describe('디자인 토큰', () => {
  const themes = loadThemes();

  for (const [themeName, tokens] of Object.entries(themes)) {
    describe(`${themeName} 테마`, () => {
      it.each(TEXT_PAIRS)('%s 글자는 %s 바탕에서 4.5:1 이상이다', (fg, bg) => {
        expect(tokens[fg], `--${fg} 토큰이 없다`).toBeDefined();
        expect(tokens[bg], `--${bg} 토큰이 없다`).toBeDefined();
        expect(contrast(tokens[fg]!, tokens[bg]!)).toBeGreaterThanOrEqual(4.5);
      });

      it.each(UI_PAIRS)('%s 선은 %s 바탕에서 3:1 이상이다', (fg, bg) => {
        expect(contrast(tokens[fg]!, tokens[bg]!)).toBeGreaterThanOrEqual(3);
      });
    });
  }

  it('설치 화면의 테마색과 HTML의 theme-color가 배경 토큰과 같다', () => {
    const pwaConfig = read('pwa.config.ts');
    const html = read('index.html');

    expect(pwaConfig).toContain(`THEME_COLOR = '${themes.light.background}'`);
    expect(html).toContain(
      `media="(prefers-color-scheme: light)" content="${themes.light.background}"`,
    );
    expect(html).toContain(
      `media="(prefers-color-scheme: dark)" content="${themes.dark.background}"`,
    );
  });

  it('문서 언어가 한국어로 지정되어 있다', () => {
    expect(read('index.html')).toContain('<html lang="ko">');
  });
});
