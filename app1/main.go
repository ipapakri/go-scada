package main

import (
	"log"

	"go-scada/stream"
)

func main() {
	streamClient, err := stream.New(
		stream.DefaultConfigPath,
		stream.WithErrorHandler(func(err error) {
			log.Printf("Stream error: %v", err)
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer streamClient.Close()

	subject := "AlertProperties.Alarm"
	json := `
	  {
  "version": 1,
  "color": "#ef4444",
  "abbreviation": "ALM",
  "short_sign": "A",
  "priority": 100,
  "requires_acknowledgement": true
}
	  `

	err = stream.Set[string](streamClient, subject, json)
	if err != nil {
		log.Fatal(err)
	}

	subject = "sensor2.value.alert_config"
	json = `
	{
  "version": 1,
  "enabled": true,
  "type": "value",
  "value": {
    "value_type": "float64",
    "intervals": [
      {
        "min": null,
        "max": 80,
        "active": false,
        "text": "Tank temperature is normal"
      },
      {
        "min": 80,
        "max": null,
        "active": true,
        "property": "AlertProperties.Alarm",
        "text": "Tank temperature is high"
      }
    ]
  }
}
`
	err = stream.Set[string](streamClient, subject, json)
	if err != nil {
		log.Fatal(err)
	}
}
