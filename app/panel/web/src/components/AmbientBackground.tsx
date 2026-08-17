export function AmbientBackground({ center = false }: { center?: boolean }) {
  return (
    <div
      className={center ? "ambient-bg ambient-bg--center" : "ambient-bg"}
      aria-hidden="true"
    />
  );
}
