package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	_ "strings"
	"time"

	"github.com/spf13/viper"
	"golang.org/x/net/websocket"
	_ "gopkg.in/yaml.v2"
)

type httpProxyConfig struct {
	SourceAddress string `yaml:"source_address"`
	TargetHost    string `yaml:"target_host"`
	TargetPort    int    `yaml:"target_port"`
	LocalPort     int    `yaml:"local_port"`
}

type webSocketProxyConfig struct {
	SourceAddress string `yaml:"source_address"`
	TargetHost    string `yaml:"target_host"`
	TargetPort    int    `yaml:"target_port"`
	LocalPort     int    `yaml:"local_port"`
	Secure        bool   `yaml:"secure"`
}

func main() {
	// Load configuration from file
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig()
	if err != nil {
		panic(fmt.Errorf("failed to read config file: %w", err))
	}

	// Start TCP proxies
	tcpProxies := viper.Get("tcp").([]interface{})
	for _, tcpProxy := range tcpProxies {
		config, ok := tcpProxy.(map[string]interface{})
		if !ok {
			log.Fatal("tcpProxy config error")
		}
		localPort := config["local_port"].(int)
		sourceAddress := config["source_address"].(string)
		targetAddress := config["target_address"].(string)

		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", localPort))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}

		fmt.Printf("Started TCP proxy on port %d\n", localPort)
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					continue
				}

				if len(sourceAddress) > 0 {
					targetAddr, _ := net.ResolveTCPAddr("tcp", targetAddress)
					targetTCPAddr := &net.TCPAddr{IP: net.ParseIP(targetAddr.IP.String()), Port: targetAddr.Port}
					sourceAddr, _ := net.ResolveTCPAddr("tcp", sourceAddress)
					sourceTCPAddr := &net.TCPAddr{IP: net.ParseIP(sourceAddr.IP.String()), Port: sourceAddr.Port}
					boundConn, err := net.DialTCP("tcp", sourceTCPAddr, targetTCPAddr)
					if err != nil {
						fmt.Fprintln(os.Stderr, err)
						conn.Close()
						continue
					}

					fmt.Printf("Proxying TCP connection from %s to %s through %s\n", conn.RemoteAddr(), targetAddress, sourceAddress)

					go func() {
						_, err := io.Copy(boundConn, conn)
						if err != nil {
							fmt.Fprintln(os.Stderr, err)
						}
						conn.Close()
						boundConn.Close()
					}()

					go func() {
						_, err := io.Copy(conn, boundConn)
						if err != nil {
							fmt.Fprintln(os.Stderr, err)
						}
						conn.Close()
						boundConn.Close()
					}()
				} else {
					targetConn, err := net.Dial("tcp", targetAddress)
					if err != nil {
						fmt.Fprintln(os.Stderr, err)
						conn.Close()
						continue
					}

					fmt.Printf("Proxying TCP connection from %s to %s\n", conn.RemoteAddr(), targetAddress)

					go func() {
						_, err := io.Copy(targetConn, conn)
						if err != nil {
							fmt.Fprintln(os.Stderr, err)
						}
						conn.Close()
						targetConn.Close()
					}()

					go func() {
						_, err := io.Copy(conn, targetConn)
						if err != nil {
							fmt.Fprintln(os.Stderr, err)
						}
						conn.Close()
						targetConn.Close()
					}()
				}
			}
		}()
	}
	// Start HTTP proxies
	httpProxies := viper.Get("http").([]interface{})
	for _, httpProxy := range httpProxies {
		config, ok := httpProxy.(map[string]interface{})
		if !ok {
			log.Fatal("httpProxy config error")
		}
		localPort := config["local_port"].(int)
		sourceAddress := config["source_address"].(string)
		targetHost := config["target_host"].(string)
		targetPort := config["target_port"].(int)

		mux := http.NewServeMux()

		if len(sourceAddress) > 0 {
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				targetURL := fmt.Sprintf("http://%s:%d%s", targetHost, targetPort, r.RequestURI)
				req, err := http.NewRequest(r.Method, targetURL, r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				req.Header = r.Header
				req.Host = targetHost

				sourceAddr, _ := net.ResolveTCPAddr("tcp", sourceAddress)
				proxy := &httputil.ReverseProxy{
					Director: func(req *http.Request) {
						req.URL.Scheme = "http"
						req.URL.Host = targetHost
						req.Host = targetHost
					},
					Transport: &http.Transport{
						Dial: func(network, addr string) (net.Conn, error) {
							conn, err := net.DialTCP("tcp", sourceAddr, &net.TCPAddr{IP: net.ParseIP(targetHost), Port: targetPort})
							if err != nil {
								return nil, err
							}
							return conn, nil
						},
					},
				}
				proxy.ServeHTTP(w, req)
			})
		} else {
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				targetURL := fmt.Sprintf("http://%s:%d%s", targetHost, targetPort, r.RequestURI)
				req, err := http.NewRequest(r.Method, targetURL, r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				req.Header = r.Header
				req.Host = targetHost

				proxy := &httputil.ReverseProxy{
					Director: func(req *http.Request) {
						req.URL.Scheme = "http"
						req.URL.Host = targetHost
						req.Host = targetHost
					},
				}
				proxy.ServeHTTP(w, req)
			})
		}

		server := &http.Server{Addr: fmt.Sprintf(":%d", localPort), Handler: mux}
		go func() {
			err := server.ListenAndServe()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}()
		fmt.Printf("Started HTTP proxy on port %d\n", localPort)
	}
	// Start WebSocket proxies
	wsProxies := viper.Get("ws").([]interface{})
	for _, wsProxy := range wsProxies {
		config, ok := wsProxy.(map[string]interface{})
		if !ok {
			log.Fatal("wsProxy config error")
		}
		localPort := config["local_port"].(int)
		sourceAddress := config["source_address"].(string)
		targetHost := config["target_host"].(string)
		targetPort := config["target_port"].(int)

		httpMux := http.NewServeMux()
		wsMux := http.NewServeMux()

		if len(sourceAddress) > 0 {
			wsMux.Handle("/", websocket.Handler(func(ws *websocket.Conn) {
				targetURL := fmt.Sprintf("ws://%s:%d%s", targetHost, targetPort, ws.Request().RequestURI)
				targetWS, err := websocket.Dial(targetURL, "", fmt.Sprintf("http://%s:%d", sourceAddress, localPort))
				if err != nil {
					http.Error(ws, err.Error(), http.StatusInternalServerError)
					return
				}
				go io.Copy(ws, targetWS)
				io.Copy(targetWS, ws)
			}))
		} else {
			wsMux.Handle("/", websocket.Handler(func(ws *websocket.Conn) {
				targetURL := fmt.Sprintf("ws://%s:%d%s", targetHost, targetPort, ws.Request().RequestURI)
				targetWS, err := websocket.Dial(targetURL, "", ws.Request().Header.Get("Origin"))
				if err != nil {
					http.Error(ws, err.Error(), http.StatusInternalServerError)
					return
				}
				go io.Copy(ws, targetWS)
				io.Copy(targetWS, ws)
			}))
		}

		httpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			targetURL := fmt.Sprintf("http://%s:%d%s", targetHost, targetPort, r.RequestURI)
			req, err := http.NewRequest(r.Method, targetURL, r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			req.Header = r.Header
			req.Host = targetHost

			sourceAddr, _ := net.ResolveTCPAddr("tcp", sourceAddress)
			proxy := &httputil.ReverseProxy{
				Director: func(req *http.Request) {
					req.URL.Scheme = "http"
					req.URL.Host = targetHost
					req.Host = targetHost
				},
				Transport: &http.Transport{
					Dial: func(network, addr string) (net.Conn, error) {
						conn, err := net.DialTCP("tcp", sourceAddr, &net.TCPAddr{IP: net.ParseIP(targetHost), Port: targetPort})
						if err != nil {
							return nil, err
						}
						return conn, nil
					},
				},
			}
			proxy.ServeHTTP(w, req)
		})
		server := &http.Server{Addr: fmt.Sprintf(":%d", localPort), Handler: httpMux}
		go func() {
			err := server.ListenAndServe()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}()

		wsServer := &http.Server{Addr: fmt.Sprintf(":%d", localPort), Handler: wsMux}
		go func() {
			err := wsServer.ListenAndServe()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}()
		fmt.Printf("Started WebSocket proxy on port %d\n", localPort)
	}
	for {
		time.Sleep(time.Second)
	}
}
