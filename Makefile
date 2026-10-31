.PHONY: dev

dev: export NALA_CTRL_DEV_HEADLESS_API = 1
dev:
	wails dev
