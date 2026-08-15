# Panel design system (T-120 round 3): Tailwind CSS toolchain.
# The compiled artifact internal/web/static/tailwind.css is COMMITTED
# (go:embed needs it at build time), so Go builds and tests stay
# offline. Node is required only to regenerate the design layer.

.PHONY: css web-build

css:
	npm ci && npm run build:css

web-build:
	cd web && npm ci && npm run build
