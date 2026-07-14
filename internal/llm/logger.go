package llm

import (
	"log"
	"os"
)

var (
	// DebugMode enables debug logging
	DebugMode = os.Getenv("SUMMONER_DEBUG") == "1"
)

func logDebug(format string, v ...interface{}) {
	if DebugMode {
		log.Printf("[LLM DEBUG] "+format, v...)
	}
}

func logInfo(format string, v ...interface{}) {
	log.Printf("[LLM] "+format, v...)
}

func logWarn(format string, v ...interface{}) {
	log.Printf("[LLM WARN] "+format, v...)
}
