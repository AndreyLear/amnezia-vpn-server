import { PlusIcon } from "lucide-react";

import { Button } from "@/components/ui/button";

type EmptyClientsProps = {
  onAdd: () => void;
};

export function EmptyClients({ onAdd }: EmptyClientsProps) {
  return (
    <div className="flex min-h-[calc(100svh-8rem)] flex-col items-center justify-center gap-4 pb-8 max-sm:pb-28">
      <style>{`
        @keyframes empty-cat-blink {
          0%, 88%, 100% { transform: scaleY(1); }
          92%, 96% { transform: scaleY(0.06); }
        }
        @keyframes empty-cat-wag {
          0%, 100% { transform: rotate(10deg); }
          50% { transform: rotate(-16deg); }
        }
        .empty-cat-lid {
          transform-box: fill-box;
          transform-origin: 50% 70%;
          animation: empty-cat-blink 5.5s ease-in-out infinite;
        }
        .empty-cat-tail {
          transform-origin: 88px 90px;
          animation: empty-cat-wag 2.6s ease-in-out infinite;
        }
        @media (prefers-reduced-motion: reduce) {
          .empty-cat-lid,
          .empty-cat-tail {
            animation: none;
          }
        }
      `}</style>
      <svg
        aria-hidden="true"
        className="size-28 text-muted-foreground"
        viewBox="0 0 128 128"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.6"
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <g className="empty-cat-tail">
          <path d="M88 90c16-6 26-22 20-40 8 12 0 34-16 42" />
        </g>
        <ellipse cx="64" cy="88" rx="28" ry="22" />
        <circle cx="64" cy="46" r="24" />
        <path d="M44 32 36 12 58 28Z" />
        <path d="M84 32 92 12 70 28Z" />
        <ellipse cx="50" cy="108" rx="10" ry="7" />
        <ellipse cx="78" cy="108" rx="10" ry="7" />
        <g className="empty-cat-lid">
          <ellipse cx="54" cy="46" rx="3.4" ry="5" fill="currentColor" stroke="none" />
        </g>
        <g className="empty-cat-lid">
          <ellipse cx="74" cy="46" rx="3.4" ry="5" fill="currentColor" stroke="none" />
        </g>
        <path d="M64 52l-3 4h6z" fill="currentColor" stroke="none" />
        <path d="M64 56.5v3.2M64 59.5q-7 5-11 1.5M64 59.5q7 5 11 1.5" />
      </svg>
      <p className="text-muted-foreground">Пока нет клиентов</p>
      <Button type="button" onClick={onAdd}>
        <PlusIcon />
        Добавить клиента
      </Button>
    </div>
  );
}
