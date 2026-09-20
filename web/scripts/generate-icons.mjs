// 자리 표시용 앱 아이콘을 만든다. 새벽 하늘 위로 해가 떠오르는 단순한 그림이다.
// 이미지 라이브러리를 의존성에 넣지 않으려고 PNG를 직접 인코딩한다.
// 정식 아이콘이 나오면 public/icons의 파일을 바꾸고 이 스크립트는 지운다.
import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { deflateSync } from 'node:zlib';

const OUT_DIR = join(dirname(fileURLToPath(import.meta.url)), '..', 'public', 'icons');

const SKY_TOP = [0xfc, 0xe8, 0xd8];
const SKY_BOTTOM = [0xf0, 0xa5, 0x7e];
const SUN = [0xff, 0xfb, 0xf6];
const GROUND = [0xb4, 0x53, 0x2d];

const HORIZON = 0.62;
const SUN_RADIUS = 0.2;
// 마스크 아이콘은 가운데 80% 원 안쪽만 보장되므로 해를 그 안에 둔다.
const CORNER_RADIUS = 0.22;
const SAMPLES = 3;

const CRC_TABLE = Array.from({ length: 256 }, (_, n) => {
  let c = n;
  for (let k = 0; k < 8; k += 1) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});

function crc32(buffer) {
  let c = 0xffffffff;
  for (const byte of buffer) c = CRC_TABLE[(c ^ byte) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function chunk(type, data) {
  const body = Buffer.concat([Buffer.from(type, 'ascii'), data]);
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body));
  return Buffer.concat([length, body, crc]);
}

function encodePng(size, rgba) {
  const header = Buffer.alloc(13);
  header.writeUInt32BE(size, 0);
  header.writeUInt32BE(size, 4);
  header.set([8, 6, 0, 0, 0], 8);

  const stride = size * 4;
  const raw = Buffer.alloc((stride + 1) * size);
  for (let y = 0; y < size; y += 1) {
    rgba.copy(raw, y * (stride + 1) + 1, y * stride, (y + 1) * stride);
  }

  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', header),
    chunk('IDAT', deflateSync(raw, { level: 9 })),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

function insideRoundedSquare(x, y, radius) {
  const dx = Math.max(radius - x, 0, x - (1 - radius));
  const dy = Math.max(radius - y, 0, y - (1 - radius));
  return dx * dx + dy * dy <= radius * radius;
}

function colorAt(x, y) {
  if (y >= HORIZON) return GROUND;
  const dx = x - 0.5;
  const dy = y - HORIZON;
  if (dx * dx + dy * dy <= SUN_RADIUS * SUN_RADIUS) return SUN;
  const t = y / HORIZON;
  return SKY_TOP.map((from, i) => from + (SKY_BOTTOM[i] - from) * t);
}

function render(size, { rounded }) {
  const rgba = Buffer.alloc(size * size * 4);
  const total = SAMPLES * SAMPLES;

  for (let py = 0; py < size; py += 1) {
    for (let px = 0; px < size; px += 1) {
      const sum = [0, 0, 0];
      let covered = 0;

      for (let sy = 0; sy < SAMPLES; sy += 1) {
        for (let sx = 0; sx < SAMPLES; sx += 1) {
          const x = (px + (sx + 0.5) / SAMPLES) / size;
          const y = (py + (sy + 0.5) / SAMPLES) / size;
          if (rounded && !insideRoundedSquare(x, y, CORNER_RADIUS)) continue;
          const color = colorAt(x, y);
          sum[0] += color[0];
          sum[1] += color[1];
          sum[2] += color[2];
          covered += 1;
        }
      }

      const offset = (py * size + px) * 4;
      if (covered === 0) continue;
      rgba[offset] = Math.round(sum[0] / covered);
      rgba[offset + 1] = Math.round(sum[1] / covered);
      rgba[offset + 2] = Math.round(sum[2] / covered);
      rgba[offset + 3] = Math.round((covered / total) * 255);
    }
  }

  return encodePng(size, rgba);
}

const TARGETS = [
  { file: 'icon-192.png', size: 192, rounded: true },
  { file: 'icon-512.png', size: 512, rounded: true },
  { file: 'maskable-192.png', size: 192, rounded: false },
  { file: 'maskable-512.png', size: 512, rounded: false },
  // iOS는 모서리를 직접 둥글리므로 투명한 부분 없이 꽉 채운다.
  { file: 'apple-touch-icon.png', size: 180, rounded: false },
];

mkdirSync(OUT_DIR, { recursive: true });
for (const { file, size, rounded } of TARGETS) {
  writeFileSync(join(OUT_DIR, file), render(size, { rounded }));
  process.stdout.write(`${file} (${size}x${size})\n`);
}
