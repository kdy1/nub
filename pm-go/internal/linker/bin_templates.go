package linker

// Wrapper text follows the reference linker. Command wrappers convert LF to CRLF.

const cmdDirect = `@SETLOCAL
{node_path}@"%~dp0\{rel_target_backslash}" %*
`

const cmdInterpreter = `@SETLOCAL
{node_path}@IF EXIST "%~dp0\{prog}.exe" (
  "%~dp0\{prog}.exe" "%~dp0\{rel_target_backslash}" %*
) ELSE (
  @SET PATHEXT=%PATHEXT:;.JS;=;%
  {prog} "%~dp0\{rel_target_backslash}" %*
)
`

const powershellDirect = `#!/usr/bin/env pwsh
$basedir=Split-Path $MyInvocation.MyCommand.Definition -Parent
{node_path}$ret=0
if ($MyInvocation.ExpectingInput) {
  $input | & "$basedir/{rel_target_fwdslash}" $args
} else {
  & "$basedir/{rel_target_fwdslash}" $args
}
$ret=$LASTEXITCODE
exit $ret
`

const powershellInterpreter = `#!/usr/bin/env pwsh
$basedir=Split-Path $MyInvocation.MyCommand.Definition -Parent

{node_path}$exe=""
if ($PSVersionTable.PSVersion -lt "6.0" -or $IsWindows) {
  $exe=".exe"
}
$ret=0
if (Test-Path "$basedir/{prog}$exe") {
  if ($MyInvocation.ExpectingInput) {
    $input | & "$basedir/{prog}$exe" "$basedir/{rel_target_fwdslash}" $args
  } else {
    & "$basedir/{prog}$exe" "$basedir/{rel_target_fwdslash}" $args
  }
  $ret=$LASTEXITCODE
} else {
  if ($MyInvocation.ExpectingInput) {
    $input | & "{prog}$exe" "$basedir/{rel_target_fwdslash}" $args
  } else {
    & "{prog}$exe" "$basedir/{rel_target_fwdslash}" $args
  }
  $ret=$LASTEXITCODE
}
exit $ret
`

const gitbashDirect = `#!/bin/sh
basedir=$(dirname "$(echo "$0" | sed -e 's,\\,/,g')")

case ` + "`" + `uname` + "`" + ` in
    *CYGWIN*|*MINGW*|*MSYS*)
        if command -v cygpath > /dev/null 2>&1; then
            basedir=` + "`" + `cygpath -w "$basedir"` + "`" + `
        fi
    ;;
esac

{node_path}exec "$basedir/{rel_target_fwdslash}" "$@"
`

const gitbashInterpreter = `#!/bin/sh
basedir=$(dirname "$(echo "$0" | sed -e 's,\\,/,g')")

case ` + "`" + `uname` + "`" + ` in
    *CYGWIN*|*MINGW*|*MSYS*)
        if command -v cygpath > /dev/null 2>&1; then
            basedir=` + "`" + `cygpath -w "$basedir"` + "`" + `
        fi
    ;;
esac

{node_path}{launch_block}`

const posixDirect = `#!/bin/sh
{POSIX_SHIM_MARKER_PREFIX}{rel_target_fwdslash}
{POSIX_SHIM_BASEDIR}{node_path}exec "$basedir/{rel_target_fwdslash}" "$@"
`

const posixInterpreter = `#!/bin/sh
{POSIX_SHIM_MARKER_PREFIX}{rel_target_fwdslash}
{POSIX_SHIM_BASEDIR}{node_path}{launch_block}`

const interpreterBlock = `if [ -x "$basedir/{prog}" ] && [ "$(command -p head -c 2 "$basedir/{prog}" 2>/dev/null || echo '#!')" != '#!' ]; then
  exec "$basedir/{prog}" "$basedir/{rel_target_fwdslash}" "$@"
else
  exec {prog} "$basedir/{rel_target_fwdslash}" "$@"
fi
`

const posixBasedir = `link="$0"
hops=0
while [ -L "$link" ] && [ "$hops" -lt 40 ]; do
hops=$((hops+1))
target=$(readlink "$link")
case "$target" in
/*) link="$target" ;;
*)  link="$(dirname "$link")/$target" ;;
esac
done
basedir=$(dirname "$link")
`
