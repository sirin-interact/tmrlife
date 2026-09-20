import { PhoneIcon } from 'lucide-react';

import type { Resource } from '@/api/types';
import { buttonVariants } from '@/components/ui/button';
import { telHref } from '@/lib/tel';
import { cn } from '@/lib/utils';

interface ResourceListProps {
  resources: readonly Resource[];
  /** 대화 화면 위쪽에 고정할 때는 자리를 덜 차지하게 그린다. */
  compact?: boolean;
  className?: string;
}

/** 도움을 받을 수 있는 곳의 목록. 번호를 누르면 바로 전화가 걸린다. */
export function ResourceList({ resources, compact = false, className }: ResourceListProps) {
  return (
    <ul className={cn('flex flex-col', compact ? 'gap-2' : 'gap-3', className)}>
      {resources.map((resource) => {
        const href = telHref(resource.phone);
        return (
          <li
            key={resource.id}
            className={cn(
              'flex items-center gap-3 rounded-xl border bg-card',
              compact ? 'py-1.5 pr-1.5 pl-4' : 'px-5 py-4',
            )}
          >
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="leading-snug font-semibold">{resource.name}</span>
              {resource.description !== '' && (
                <span
                  className={cn(
                    'text-sm leading-relaxed text-muted-foreground',
                    // 고정 영역에서는 화면이 낮으면(키보드가 올라온 폰) 설명을 접어 대화가 보일 자리를 남긴다.
                    compact && '[@media(max-height:720px)]:hidden',
                  )}
                >
                  {resource.description}
                </span>
              )}
            </div>
            {href === null ? (
              <span className="px-3 font-semibold">{resource.phone}</span>
            ) : (
              <a
                href={href}
                aria-label={`${resource.name} ${resource.phone} 전화하기`}
                className={cn(
                  buttonVariants({ variant: compact ? 'outline' : 'default' }),
                  'shrink-0 px-4 tabular-nums',
                )}
              >
                <PhoneIcon aria-hidden="true" className="size-4" />
                {resource.phone}
              </a>
            )}
          </li>
        );
      })}
    </ul>
  );
}
