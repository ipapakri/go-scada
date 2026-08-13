package main

import (
	"log"

	"go-scada/stream"
)

func floatHandler(subject string, value float64) error {
	log.Printf("Received on %q: %.2f °C", subject, value)
	return nil
}

func boolHandler(subject string, value bool) error {
	log.Printf("Received on %q: %v", subject, value)
	return nil
}

func int64Handler(subject string, value int64) error {
	log.Printf("Received on %q: %d", subject, value)
	return nil
}

func stringHandler(subject string, value string) error {
	log.Printf("Received on %q: %q", subject, value)
	return nil
}

func main() {
	log.Println("Starting app2")
	streamClient, err := stream.New(
		stream.DefaultConfigPath,
		stream.WithErrorHandler(func(err error) {
			log.Printf("Subscription error: %v", err)
		}),
	)
	if err != nil {
		log.Fatalf("Failed to create stream client: %v", err)
	}
	log.Println("Stream client created")
	defer streamClient.Close()

	/*subject := "sensor1.value"
	temperatureSubscription, err := stream.Subscribe(
		streamClient,
		subject,
		floatHandler,
	)
	if err != nil {
		log.Fatal(err)
	}
	defer temperatureSubscription.Stop()*/

	statusSubject := "scada.demo.status"
	//stateSubject := "scada.demo.state"
	stateSubject := "sensor1.value"
	messageSubject := "scada.demo.message"

	statusSubscription, err := stream.Subscribe(
		streamClient,
		statusSubject,
		boolHandler,
	)
	if err != nil {
		log.Fatal(err)
	}
	defer statusSubscription.Stop()

	stateSubscription, err := stream.Subscribe(
		streamClient,
		stateSubject,
		int64Handler,
	)
	if err != nil {
		log.Fatal(err)
	}
	defer stateSubscription.Stop()

	messageSubscription, err := stream.Subscribe(
		streamClient,
		messageSubject,
		stringHandler,
	)
	if err != nil {
		log.Fatal(err)
	}
	defer messageSubscription.Stop()

	select {}
}
