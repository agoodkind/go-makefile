# go-service.mk: cross-platform user service install/uninstall.
#
# Project Makefile must set:
#   LAUNCHD_LABEL := io.goodkind.<name>     # macOS launchd label
#   SYSTEMD_UNIT  := <name>.service          # Linux systemd user unit name
#
# Project must provide unit templates at:
#   packaging/macos/$(LAUNCHD_LABEL).plist.in
#   packaging/systemd/$(SYSTEMD_UNIT).in
#
# Templates use @@VAR@@ markers, replaced at install time:
#   @@BIN_PATH@@   absolute path to installed binary ($(INSTALL_BIN) from go-build.mk)
#   @@HOME@@       $HOME
#   @@LABEL@@      $(LAUNCHD_LABEL) or $(SYSTEMD_UNIT) without .service
#   @@LOG_PATH@@   $(LOG_PATH) (default ~/Library/Logs/$(BINARY).log on macOS)
#
# Targets:
#   service-install     render template, then bootstrap (macOS) or enable+start (Linux)
#   service-uninstall   bootout + remove plist (macOS) or disable+remove unit (Linux)
#   service-restart     restart the running service
#   service-status      show service status

.PHONY: service-install service-uninstall service-restart service-status \
	service-render service-check

ifeq ($(strip $(LAUNCHD_LABEL))$(strip $(SYSTEMD_UNIT)),)
$(error go-service.mk: set LAUNCHD_LABEL and/or SYSTEMD_UNIT in the project Makefile)
endif

LAUNCHD_PLIST    ?= $(HOME)/Library/LaunchAgents/$(LAUNCHD_LABEL).plist
LAUNCHD_TEMPLATE ?= packaging/macos/$(LAUNCHD_LABEL).plist.in
LAUNCHD_DOMAIN   ?= gui/$(shell id -u)

SYSTEMD_USER_DIR  ?= $(HOME)/.config/systemd/user
SYSTEMD_USER_UNIT ?= $(SYSTEMD_USER_DIR)/$(SYSTEMD_UNIT)
SYSTEMD_TEMPLATE  ?= packaging/systemd/$(SYSTEMD_UNIT).in

LOG_PATH ?= $(HOME)/Library/Logs/$(BINARY).log

# Render a template by replacing @@VAR@@ markers via sed. $(1) source, $(2) dest.
define render_service_template
	@mkdir -p "$(dir $(2))"
	@sed -e 's|@@BIN_PATH@@|$(INSTALL_BIN)|g' \
	     -e 's|@@HOME@@|$(HOME)|g' \
	     -e 's|@@LABEL@@|$(if $(LAUNCHD_LABEL),$(LAUNCHD_LABEL),$(basename $(SYSTEMD_UNIT)))|g' \
	     -e 's|@@LOG_PATH@@|$(LOG_PATH)|g' \
	     "$(1)" > "$(2)"
endef

service-check:
	@if [ "$$(uname)" = "Darwin" ]; then \
		[ -n "$(LAUNCHD_LABEL)" ] || { echo "service-install: LAUNCHD_LABEL not set" >&2; exit 1; }; \
		[ -f "$(LAUNCHD_TEMPLATE)" ] || { echo "service-install: $(LAUNCHD_TEMPLATE) not found" >&2; exit 1; }; \
	else \
		[ -n "$(SYSTEMD_UNIT)" ] || { echo "service-install: SYSTEMD_UNIT not set" >&2; exit 1; }; \
		[ -f "$(SYSTEMD_TEMPLATE)" ] || { echo "service-install: $(SYSTEMD_TEMPLATE) not found" >&2; exit 1; }; \
		command -v systemctl >/dev/null 2>&1 || { echo "service-install: systemctl not found" >&2; exit 1; }; \
	fi

service-install: service-check
	@if [ "$$(uname)" = "Darwin" ]; then \
		mkdir -p "$(HOME)/Library/LaunchAgents" "$(HOME)/Library/Logs"; \
		touch "$(LOG_PATH)"; \
		sed -e 's|@@BIN_PATH@@|$(INSTALL_BIN)|g' \
		    -e 's|@@HOME@@|$(HOME)|g' \
		    -e 's|@@LABEL@@|$(LAUNCHD_LABEL)|g' \
		    -e 's|@@LOG_PATH@@|$(LOG_PATH)|g' \
		    "$(LAUNCHD_TEMPLATE)" > "$(LAUNCHD_PLIST)"; \
		launchctl bootout $(LAUNCHD_DOMAIN) "$(LAUNCHD_PLIST)" 2>/dev/null; true; \
		launchctl bootstrap $(LAUNCHD_DOMAIN) "$(LAUNCHD_PLIST)"; \
		echo "installed: $(LAUNCHD_PLIST)"; \
		echo "  logs: $(LOG_PATH)"; \
	else \
		mkdir -p "$(SYSTEMD_USER_DIR)"; \
		sed -e 's|@@BIN_PATH@@|$(INSTALL_BIN)|g' \
		    -e 's|@@HOME@@|$(HOME)|g' \
		    -e 's|@@LABEL@@|$(basename $(SYSTEMD_UNIT))|g' \
		    -e 's|@@LOG_PATH@@|$(LOG_PATH)|g' \
		    "$(SYSTEMD_TEMPLATE)" > "$(SYSTEMD_USER_UNIT)"; \
		systemctl --user daemon-reload; \
		systemctl --user enable "$(SYSTEMD_UNIT)"; \
		systemctl --user restart "$(SYSTEMD_UNIT)"; \
		echo "installed: $(SYSTEMD_USER_UNIT)"; \
		echo "  logs: journalctl --user -u $(SYSTEMD_UNIT) -f"; \
	fi

service-uninstall:
	@if [ "$$(uname)" = "Darwin" ]; then \
		launchctl bootout $(LAUNCHD_DOMAIN) "$(LAUNCHD_PLIST)" 2>/dev/null; true; \
		rm -f "$(LAUNCHD_PLIST)"; \
		echo "removed: $(LAUNCHD_PLIST)"; \
	else \
		command -v systemctl >/dev/null 2>&1 || { echo "service-uninstall: systemctl not found" >&2; exit 1; }; \
		systemctl --user disable --now "$(SYSTEMD_UNIT)" 2>/dev/null; true; \
		rm -f "$(SYSTEMD_USER_UNIT)"; \
		systemctl --user daemon-reload; \
		echo "removed: $(SYSTEMD_USER_UNIT)"; \
	fi

service-restart:
	@if [ "$$(uname)" = "Darwin" ]; then \
		launchctl kickstart -k $(LAUNCHD_DOMAIN)/$(LAUNCHD_LABEL); \
	else \
		systemctl --user restart "$(SYSTEMD_UNIT)"; \
	fi

service-status:
	@if [ "$$(uname)" = "Darwin" ]; then \
		launchctl print $(LAUNCHD_DOMAIN)/$(LAUNCHD_LABEL) 2>/dev/null | head -20 || echo "not loaded: $(LAUNCHD_LABEL)"; \
	else \
		systemctl --user status "$(SYSTEMD_UNIT)" --no-pager || true; \
	fi
