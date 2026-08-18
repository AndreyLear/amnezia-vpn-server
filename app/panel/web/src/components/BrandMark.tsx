import { cn } from "@/lib/utils";

export function BrandMark({ className }: { className?: string }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 32 32"
      role="img"
      aria-label="AWG Panel"
      className={cn(className)}
    >
      <rect width="32" height="32" rx="8" className="fill-foreground" />
      <path
        className="fill-background"
        fillRule="evenodd"
        d="M16 6.5 25.5 25.5h-3.4l-2.05-4.7h-8.1L9.9 25.5H6.5L16 6.5Zm0 5.7-2.85 6.6h5.7L16 12.2Z"
      />
    </svg>
  );
}
