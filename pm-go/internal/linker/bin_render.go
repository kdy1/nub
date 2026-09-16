package linker

import "strings"

const posixShimMarker = "# aube-bin-shim v2 target="

func renderBinShim(style string, launch binLaunch, target, nodePath string) string {
	program := launch.program
	if !safeProgram(program) {
		program = "node"
	}
	block := strings.NewReplacer("{prog}", program, "{rel_target_fwdslash}", target).Replace(interpreterBlock)
	node := ""
	if nodePath != "" {
		switch style {
		case "cmd":
			node = "@SET NODE_PATH=" + nodePath + "\n"
		case "powershell":
			node = "$env:NODE_PATH=\"" + nodePath + "\"\n"
		default:
			node = "export NODE_PATH=\"" + nodePath + "\"\n"
		}
	}
	template := posixInterpreter
	switch style {
	case "cmd":
		template = cmdInterpreter
		if launch.direct {
			template = cmdDirect
		}
	case "powershell":
		template = powershellInterpreter
		if launch.direct {
			template = powershellDirect
		}
	case "gitbash":
		template = gitbashInterpreter
		if launch.direct {
			template = gitbashDirect
		}
	default:
		if launch.direct {
			template = posixDirect
		}
	}
	text := strings.NewReplacer("{node_path}", node, "{prog}", program,
		"{rel_target_backslash}", target, "{rel_target_fwdslash}", target,
		"{launch_block}", block, "{POSIX_SHIM_MARKER_PREFIX}", posixShimMarker,
		"{POSIX_SHIM_BASEDIR}", posixBasedir).Replace(template)
	if style == "cmd" {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}
