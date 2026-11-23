// Stream the current 15-minute BTC up-or-down market: book+ and trades+, with the probnode object.
//
//	PROBNODE_API_KEY=... go run ./examples/stream
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	probnode "github.com/probnode/probnode-go"
)

func main() {
	ctx := context.Background()
	c, err := probnode.DialWith(ctx, "wss://live.probnode.com/v2/live", os.Getenv("PROBNODE_API_KEY"),
		probnode.Options{Format: probnode.JSON, Reconnect: true})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	market := fmt.Sprintf("btc-updown-15m-%d", time.Now().Unix()/900*900)
	if err := c.Subscribe(ctx, []string{market}, []string{"book+", "trades+"}, nil); err != nil {
		log.Fatal(err)
	}
	for {
		m, err := c.Next(ctx)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(m.JSON["type"])
	}
}
