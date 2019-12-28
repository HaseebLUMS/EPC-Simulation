package main

import (
	"flag"
	"fmt"
	"log"
	"net/rpc"
	"strconv"
	"tinyepc/rpcs"
)

var (
	lbPort = flag.Int("port", 8000, "Port of the LoadBalancer")
)

func init() {
	log.SetFlags(log.Lshortfile | log.Lmicroseconds)
}

func main() {
	flag.Parse()

	lbAddr := ":" + strconv.Itoa(*lbPort)
	_ = lbAddr

	// TODO: Implement this!

	client, dialError := rpc.DialHTTP("tcp", "localhost"+lbAddr)
	if dialError != nil {
		return
	}
	joinReply := rpcs.UERequestReply{}
	client.Call("LoadBalancer.RecvUERequest", rpcs.UERequestArgs{10, 0}, &joinReply)
	client.Call("LoadBalancer.RecvUERequest", rpcs.UERequestArgs{10, 2}, &joinReply)
	client.Call("LoadBalancer.RecvUERequest", rpcs.UERequestArgs{10, 1}, &joinReply)
	client.Call("LoadBalancer.RecvUERequest", rpcs.UERequestArgs{20, 0}, &joinReply)
	client.Call("LoadBalancer.RecvUERequest", rpcs.UERequestArgs{30, 0}, &joinReply)

	fmt.Println(joinReply)
}
