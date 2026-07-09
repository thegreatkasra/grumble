package main

import "log"

func markRuntimeReady(logger *log.Logger) {
	runtimeState.MarkReady()
	emitStructuredEvent(logger, "info", "runtime_ready", map[string]string{
		"listener_type": "runtime",
	})
}
