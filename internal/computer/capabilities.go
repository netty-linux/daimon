package computer

// Exact public CUA names. No substring, remote annotations, or implicit grant.
func Classify(remote string) Class {
	switch remote {
	case "list_apps", "list_windows", "get_window_state", "get_accessibility_tree", "debug_window_info", "get_desktop_state", "get_screen_size", "get_cursor_position", "zoom":
		return Observe
	case "click", "bring_to_front", "set_window_frame":
		return Navigate
	case "type_text", "press_key", "hotkey":
		return Input
	case "launch_app":
		return System
	case "kill_app", "invoke_menu":
		return Dangerous
	default:
		return Dangerous
	}
}
func supported(remote string) bool {
	switch remote {
	case "list_apps", "list_windows", "get_window_state", "get_accessibility_tree", "click", "type_text", "bring_to_front":
		return true
	default:
		return false
	}
}
