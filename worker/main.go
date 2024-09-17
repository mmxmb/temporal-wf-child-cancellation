package main

import (
	"log"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"childcancellation"
)

func main() {
	// The client and worker are heavyweight objects that should be created once per process.
	c, err := client.Dial(client.Options{
		HostPort: client.DefaultHostPort,
	})
	if err != nil {
		log.Fatalln("Unable to create client", err)
	}
	defer c.Close()

	w := worker.New(c, childcancellation.TaskQueue, worker.Options{})

	w.RegisterWorkflow(childcancellation.Parent)
	w.RegisterWorkflow(childcancellation.Child)
	w.RegisterActivity(&childcancellation.Activities{TemporalClient: c})

	err = w.Run(worker.InterruptCh())
	if err != nil {
		log.Fatalln("Unable to start worker", err)
	}
}
