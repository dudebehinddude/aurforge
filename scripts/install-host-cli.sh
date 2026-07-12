#!/bin/sh
set -eu

bin_dir="${AURFORGE_CLI_BIN_DIR:-/host-bin}"
project="${AURFORGE_COMPOSE_PROJECT:-aurforge}"
target="${bin_dir}/aurforge"

if [ ! -d "${bin_dir}" ]; then
	printf 'aurforge: host CLI bin dir missing: %s\n' "${bin_dir}" >&2
	exit 1
fi

cat >"${target}" <<EOF
#!/bin/sh
# Host-side wrapper installed while the Aurforge controller is running.
set -eu

project="${project}"
cid="\$(
	docker ps -q \\
		--filter "label=com.docker.compose.project=\${project}" \\
		--filter "label=com.docker.compose.service=controller" \\
		| head -n 1
)"

if [ -z "\${cid}" ]; then
	printf 'aurforge: controller is not running (project %s)\\n' "\${project}" >&2
	exit 1
fi

if [ -t 0 ] && [ -t 1 ]; then
	exec docker exec -it "\${cid}" aurforge "\$@"
fi
exec docker exec -i "\${cid}" aurforge "\$@"
EOF

chmod 0755 "${target}"
printf 'aurforge: installed host CLI at %s\n' "${target}" >&2
