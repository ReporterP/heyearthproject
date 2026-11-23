// Command stubcore stands in for the xray core in the tests of internal/synth: it reads the
// port of the first inbound from the config given with -c and, by SYNTH_STUB:
//
//	socks     — serves SOCKS5 CONNECT on it (a working core);
//	hold      — accepts and never answers (a core whose path is frozen);
//	nolisten  — never opens it (a core that failed to start).
//
// In every mode it also listens on SYNTH_STUB_LIVE: that port answers exactly while the
// process is alive, which is how the tests tell on any OS.
package main

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"strconv"
	"time"
)

func main() {
	if live := os.Getenv("SYNTH_STUB_LIVE"); live != "" {
		ln, err := net.Listen("tcp", "127.0.0.1:"+live)
		if err != nil {
			os.Exit(5)
		}
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}()
	}
	mode := os.Getenv("SYNTH_STUB")
	if mode == "nolisten" {
		time.Sleep(time.Hour)
		return
	}
	var path string
	for i, a := range os.Args {
		if a == "-c" && i+1 < len(os.Args) {
			path = os.Args[i+1]
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		os.Exit(2)
	}
	var cfg struct {
		Inbounds []struct {
			Port int `json:"port"`
		} `json:"inbounds"`
	}
	if json.Unmarshal(b, &cfg) != nil || len(cfg.Inbounds) == 0 {
		os.Exit(3)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(cfg.Inbounds[0].Port))
	if err != nil {
		os.Exit(4)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		if mode == "hold" {
			go io.Copy(io.Discard, c)
			continue
		}
		go socks(c)
	}
}

func socks(c net.Conn) {
	defer c.Close()
	buf := make([]byte, 262)
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return
	}
	if _, err := io.ReadFull(c, buf[:int(buf[1])]); err != nil {
		return
	}
	c.Write([]byte{5, 0})
	if _, err := io.ReadFull(c, buf[:4]); err != nil {
		return
	}
	var host string
	switch buf[3] {
	case 1:
		io.ReadFull(c, buf[:4])
		host = net.IP(buf[:4]).String()
	case 3:
		io.ReadFull(c, buf[:1])
		n := int(buf[0])
		io.ReadFull(c, buf[:n])
		host = string(buf[:n])
	default:
		return
	}
	io.ReadFull(c, buf[:2])
	port := int(buf[0])<<8 | int(buf[1])
	up, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	go io.Copy(up, c)
	io.Copy(c, up)
}
