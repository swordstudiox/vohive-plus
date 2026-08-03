#!/usr/bin/env sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

if [ -x "$script_dir/vohive" ]; then
  exec "$script_dir/vohive" --prepare-usb
fi

if [ -x /opt/vohive/bin/vohive ]; then
  exec /opt/vohive/bin/vohive --prepare-usb
fi

echo '{"supported_device_found":false,"prepared":false,"message":"未找到 vohive 二进制，无法执行 WSL USB 准备。"}'
exit 1
