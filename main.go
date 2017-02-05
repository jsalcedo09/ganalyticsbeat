package main

import (
	"os"

	"github.com/elastic/beats/libbeat/beat"

	"github.com/jsalcedo09/ganalyticsbeat/beater"
)

func main() {
	err := beat.Run("ganalyticsbeat", "", beater.New)
	if err != nil {
		os.Exit(1)
	}
}
