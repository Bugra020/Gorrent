package utils

import "log"

var debugMode bool = false

func Set_debug_mode(debug_mode bool) {
	debugMode = debug_mode
}

func Debuglog(format string, a ...interface{}) {
	if debugMode {
		log.Printf("[DEBUG] "+format+"\n", a...)
	}
}
