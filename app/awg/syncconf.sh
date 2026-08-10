#!/bin/bash
# Syncconf helpers for the awg entrypoint (M3.2).
#
# `awg syncconf` feeds the configuration straight into the AWG daemon
# parser (amneziawg-tools src/config.c), which rejects every key it does
# not know with "Line unrecognized" (and echoes the whole line). The
# wg-quick-only keywords below are consumed by `awg-quick` itself and
# must never reach `awg syncconf`:
#
#   Address, DNS, MTU, Table, PreUp, PreDown, PostUp, PostDown, SaveConfig
#
# (exactly the quick keywords of src/wg-quick/linux.bash parse_options)
# plus ListenPort: `awg syncconf` would send it to the userspace daemon,
# which then rebinds the UDP socket on every reload. The panel changes the
# listen port only via a full restart, so syncconf input never carries it.
#
# Key matching is case-insensitive, mirroring the C parser (strncasecmp)
# and awg-quick (nocasematch). Comments and blank lines are dropped.

# Space-separated lowercase quick-only keys.
AWG_QUICK_ONLY_KEYS='address dns mtu table preup postup predown postdown saveconfig listenport'

# filter_syncconf_config <file>: prints the config with quick-only keys
# removed; everything else (PrivateKey, Jc..I5, [Peer] sections, ...)
# passes through unchanged.
filter_syncconf_config() {
	awk -v quickkeys="${AWG_QUICK_ONLY_KEYS}" '
		function is_quick_key(key) {
			split(quickkeys, keys, " ")
			for (i in keys)
				if (key == keys[i])
					return 1
			return 0
		}
		/^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
		{
			key = $0
			sub(/[[:space:]]*=.*/, "", key)
			gsub(/[[:space:]]/, "", key)
			if (is_quick_key(tolower(key)))
				next
			print
		}
	' "$1"
}
