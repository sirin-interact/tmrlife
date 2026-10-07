import { AudioLines, BookOpen, Leaf, Sparkles } from 'lucide-react';
import { useId } from 'react';

/** 작은 정원처럼, 처음 찾아온 사람에게 내일의 속도를 보여 주는 장면. */
export function BrandScene() {
  const gradientId = useId();

  return (
    <aside className="auth-story" aria-label="내일 소개">
      <div className="auth-story__copy">
        <span className="auth-story__eyebrow">
          <Leaf size={14} aria-hidden="true" /> 하루 한 번, 나를 위한 시간
        </span>
        <p className="auth-story__title">
          오늘의 마음이,
          <br />
          <span>내일의 나에게.</span>
        </p>
        <p className="auth-story__description">
          잘 정리된 말이 아니어도 괜찮아요.
          <br />
          오늘 하루를 천천히 들려주세요.
        </p>
      </div>

      <div className="auth-garden" aria-hidden="true">
        <div className="auth-garden__halo auth-garden__halo--outer" />
        <div className="auth-garden__halo auth-garden__halo--inner" />
        <svg className="auth-garden__botanical" viewBox="0 0 420 340" fill="none">
          <defs>
            <linearGradient id={`${gradientId}-leaf`} x1="72" y1="104" x2="137" y2="290">
              <stop stopColor="#b5c8a8" />
              <stop offset="1" stopColor="#668368" />
            </linearGradient>
            <linearGradient id={`${gradientId}-right`} x1="302" y1="202" x2="354" y2="300">
              <stop stopColor="#b4c79f" />
              <stop offset="1" stopColor="#88a47d" />
            </linearGradient>
          </defs>
          <g className="auth-garden__branch auth-garden__branch--left">
            <path d="M136 294C115 256 101 214 97 166" stroke="#70896c" strokeWidth="2" />
            <path
              d="M100 195C72 189 61 165 68 142C92 146 108 166 100 195Z"
              fill={`url(#${gradientId}-leaf)`}
            />
            <path
              d="M107 226C113 197 134 182 154 185C155 209 135 230 107 226Z"
              fill={`url(#${gradientId}-leaf)`}
            />
            <path
              d="M117 256C88 255 65 237 65 214C91 211 115 229 117 256Z"
              fill={`url(#${gradientId}-leaf)`}
            />
            <path d="M96 166C73 149 75 123 89 111C107 126 111 149 96 166Z" fill="#c8d5b7" />
          </g>
          <g className="auth-garden__branch auth-garden__branch--right">
            <path d="M289 300C311 276 327 253 336 223" stroke="#8da27d" strokeWidth="2" />
            <path
              d="M321 258C315 235 327 211 346 207C356 229 346 249 321 258Z"
              fill={`url(#${gradientId}-right)`}
            />
            <path d="M306 280C286 279 273 261 277 245C298 246 312 259 306 280Z" fill="#a0b58e" />
            <path d="M308 279C327 261 349 261 362 275C348 293 326 294 308 279Z" fill="#c0cdaa" />
          </g>
          <ellipse cx="214" cy="304" rx="82" ry="10" fill="#a9b99b" opacity=".17" />
          <path d="M313 80V92M307 86H319" stroke="#8ba086" strokeWidth="2" strokeLinecap="round" />
          <circle cx="74" cy="86" r="3" fill="#c5b979" />
          <circle cx="352" cy="171" r="2.5" fill="#9fae88" />
        </svg>
        <div className="auth-garden__orb">
          <div className="auth-garden__wave">
            <span />
            <span />
            <span />
            <span />
            <span />
          </div>
        </div>
        <div className="auth-garden__note">
          <span className="auth-garden__note-icon">
            <AudioLines size={16} />
          </span>
          <span>어떤 하루를 보내셨나요?</span>
        </div>
        <span className="auth-garden__caption">말하는 만큼, 가벼워지는 하루</span>
      </div>

      <div className="auth-story__features">
        <span>
          <AudioLines size={16} aria-hidden="true" /> 편안한 대화
        </span>
        <span>
          <BookOpen size={16} aria-hidden="true" /> 나만의 일기
        </span>
        <span>
          <Sparkles size={16} aria-hidden="true" /> 마음 돌아보기
        </span>
      </div>
    </aside>
  );
}
