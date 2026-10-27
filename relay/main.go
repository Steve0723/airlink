package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"
)

type config struct {
	key        string
	base       string
	defaultTTL time.Duration
	maxBytes   int64
}

func main() {
	addr := flag.String("addr", ":8080", "监听地址")
	dataDir := flag.String("data", "./data", "数据目录")
	key := flag.String("key", os.Getenv("AIRLINK_RELAY_KEY"), "上传密钥（必填，也可用 AIRLINK_RELAY_KEY）")
	base := flag.String("base", os.Getenv("AIRLINK_RELAY_BASE"), "对外访问地址，如 https://drop.example.com")
	ttl := flag.Duration("ttl", 24*time.Hour, "默认有效期")
	maxMB := flag.Int64("max-mb", 2048, "单文件上限（MB）")
	flag.Parse()

	if *key == "" {
		log.Fatal("必须通过 -key 或环境变量 AIRLINK_RELAY_KEY 设置上传密钥")
	}

	store, err := NewStore(*dataDir)
	if err != nil {
		log.Fatalf("无法初始化存储: %v", err)
	}

	a := &api{
		store: store,
		cfg: &config{
			key:        *key,
			base:       *base,
			defaultTTL: *ttl,
			maxBytes:   *maxMB << 20,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/put", a.handlePut)
	mux.HandleFunc("GET /s/{code}", a.handlePage)
	mux.HandleFunc("GET /s/{code}/info", a.handleInfo)
	mux.HandleFunc("GET /s/{code}/f", a.handleDownload)
	mux.HandleFunc("GET /healthz", a.handleHealth)

	// 中转文件本来就是临时的，过期就删——这点和客户端不同。
	go func() {
		for range time.Tick(5 * time.Minute) {
			if n, freed := store.Purge(); n > 0 {
				log.Printf("清理 %d 个过期文件，释放 %.1f MB", n, float64(freed)/(1<<20))
			}
		}
	}()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("AirLink Relay · 监听 %s · 数据目录 %s · 默认有效期 %s", *addr, *dataDir, *ttl)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
