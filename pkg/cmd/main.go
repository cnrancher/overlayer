package main

import (
	"os"

	"github.com/cnrancher/overlayer/pkg/commands"
	"github.com/cnrancher/overlayer/pkg/utils"
)

func main() {
	utils.SetupLogrus()
	commands.Execute(os.Args[1:])
}
