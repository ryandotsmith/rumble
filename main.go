package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/ec2rolecreds"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/lib/pq"
	"github.com/ryandotsmith/32k.io/net/http/limit"
	"github.com/ryandotsmith/32k.io/net/mylisten"
	"github.com/ryandotsmith/32k.io/net/mytls"
	"golang.org/x/net/html"
)

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

var (
	baseURL    *url.URL
	db         *sql.DB
	awsSession *session.Session
)

func init() {
	s := session.New(&aws.Config{Region: aws.String("us-west-1")})
	c := credentials.NewChainCredentials(
		[]credentials.Provider{
			&credentials.EnvProvider{},
			&ec2rolecreds.EC2RoleProvider{
				Client: ec2metadata.New(s),
			},
		},
	)
	// Check for invalid or missing credentials
	_, err := c.Get()
	check(err)
	// Set the credentials on our session struct
	s.Config.Credentials = c
	awsSession = s
}

func main() {
	var err error

	crawlSite := flag.String("c", "", "site to crawl")
	listen := flag.String("listen", "localhost:8000", "listen `address` (if no LISTEN_FDS)")
	dir := flag.String("data", "./rumble-config", "data directory")
	flag.Parse()

	db, err = sql.Open("postgres", dburl())
	check(err)

	if *crawlSite != "" {
		fmt.Printf("crawling: %s\n", *crawlSite)
		baseURL, err = url.Parse(*crawlSite)
		check(err)
		crawl(baseURL, func(u *url.URL, doc *document) {
			if doc.productPage() {
				fmt.Printf("added: %s\n", insertProduct(u, doc))
			} else {
				fmt.Printf("skipping %s\n", u.Path)
			}
		})
		os.Exit(0)
	}

	l, r, err := mylisten.SystemdOr(*listen)
	check(err)
	if r != nil {
		go func() {
			rSrv := &http.Server{
				ReadTimeout:  5 * time.Second,
				WriteTimeout: 5 * time.Second,
				Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					w.Header().Set("Connection", "close")
					url := "https://" + req.Host + req.URL.String()
					http.Redirect(w, req, url, http.StatusMovedPermanently)
				}),
			}
			err := rSrv.Serve(r)
			panic(err)
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/edit", edit)
	mux.HandleFunc("/", view)

	var handler http.Handler
	handler = limit.NewHandler(mux, 10)
	handler = limit.MaxBytes(handler, limit.OneMB)

	// Timeout settings based on Filippo's late-2016 blog post
	// https://blog.filippo.io/exposing-go-on-the-internet/.
	srv := &http.Server{
		ReadTimeout: 5 * time.Second,
		// must be higher than the event handler timeout (10s)
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  120 * time.Second,
		Handler:      handler,
	}

	cfg, err := mytls.LocalOrLets(*dir)
	check(err)
	err = srv.Serve(tls.NewListener(l, cfg))
	check(err)
}

func edit(w http.ResponseWriter, r *http.Request) {
	q := `
		select o_title, array_agg(coalesce(images.id,''))
		from products
		left join images on images.pid = products.id
		where products.id = $1
		group by o_title
	`
	row := db.QueryRow(q, r.URL.Query().Get("id"))
	p := &product{}
	imgs := pq.StringArray{}
	err := row.Scan(&p.OTitle, &imgs)
	check(err)
	for i := range imgs {
		if imgs[i] == "" {
			continue
		}
		u := fmt.Sprintf("http://imgs.rumblegoods.com/%s", imgs[i])
		p.Images = append(p.Images, u)
	}

	dat, err := ioutil.ReadFile("edit.html")
	check(err)
	t, err := template.New("edit").Parse(string(dat))
	check(err)
	check(t.Execute(w, p))
}

func view(w http.ResponseWriter, r *http.Request) {
	funcMap := template.FuncMap{
		"open": func(i int) bool {
			return i%3 == 0
		},
		"close": func(i int) bool {
			return i%3 == 2
		},
	}
	dat, err := ioutil.ReadFile("admin.html")
	check(err)
	t, err := template.New("admin").Funcs(funcMap).Parse(string(dat))
	check(err)
	products := []*product{}
	q := `
		select o_title, array_agg(images.id)
		from products, images
		where images.pid = products.id
		group by o_title
	`
	rows, err := db.Query(q)
	check(err)
	defer rows.Close()
	for rows.Next() {
		p := &product{}
		var imgs pq.StringArray
		err := rows.Scan(&p.OTitle, &imgs)
		check(err)
		for i := range imgs {
			u := fmt.Sprintf("http://imgs.rumblegoods.com/%s", imgs[i])
			p.Images = append(p.Images, u)
		}
		products = append(products, p)
	}
	data := struct {
		Products []*product
	}{products}
	check(t.Execute(w, data))
}

func attrVal(n *html.Node, key string) string {
	if n.Type == html.ElementNode {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
	}
	return ""
}

var (
	visited = map[string]int{}
	lastReq = time.Now().UnixNano()
	delay   = int64(1000 * time.Millisecond)
)

func crawl(u *url.URL, do func(*url.URL, *document)) {
	if _, ok := visited[u.String()]; ok {
		return
	}
	visited[u.String()] = 1

	for {
		t := time.Now().UnixNano()
		if t-lastReq > delay {
			lastReq = t
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	resp, err := http.Get(u.String())
	if err != nil {
		fmt.Println(err)
		return
	}
	root, err := html.Parse(resp.Body)
	if err != nil {
		resp.Body.Close()
		fmt.Println(err)
	}
	resp.Body.Close()

	doc := newDocument(root)
	do(u, doc)
	links := doc.links()
	for i := range links {
		crawl(links[i], do)
	}
}

func visit(node *html.Node, do func(*html.Node)) {
	do(node)
	for c := node.FirstChild; c != nil; c = c.NextSibling {
		visit(c, do)
	}
}

type document struct {
	root *html.Node
	list []*html.Node
}

func newDocument(root *html.Node) *document {
	doc := &document{}
	doc.list = []*html.Node{}
	visit(root, func(n *html.Node) {
		doc.list = append(doc.list, n)
	})
	return doc
}

func (doc *document) images() []string {
	imgs := []string{}
	for _, n := range doc.list {
		if n.Type == html.ElementNode && n.Data == "a" {
			v := attrVal(n, "href")
			if u, err := url.Parse(v); err == nil {
				ext := filepath.Ext(u.Path)
				if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
					imgs = append(imgs, u.String())
				}
			}
		}
		if n.Type == html.ElementNode && n.Data == "img" {
			v := attrVal(n, "src")
			if u, err := url.Parse(v); err == nil {
				u.RawQuery = ""
				u.Fragment = ""
				if u.Host == "" {
					u.Host = baseURL.Host
				}
				if u.Scheme == "" {
					u.Scheme = baseURL.Scheme
				}
				imgs = append(imgs, u.String())
			} else {
				fmt.Println("error: %s\nsrc:%s\n", err, v)
			}
		}
	}
	return imgs
}

func (doc *document) title() string {
	title := ""
	for _, n := range doc.list {
		if n.Type == html.ElementNode && (n.Data == "h1" || n.Data == "h2" || n.Data == "h3") {
			title = title + " " + n.FirstChild.Data
		}
	}
	return strings.TrimSpace(strings.ToLower(title))
}

func (doc *document) productPage() bool {
	var cartLinks int
	for _, n := range doc.list {
		if n.Type == html.ElementNode && (n.Data == "button" || n.Data == "a") {
			visit(n, func(n1 *html.Node) {
				if strings.Contains(strings.ToLower(n1.Data), "add to cart") {
					cartLinks++
				}
			})
		}
	}
	return cartLinks == 1
}

func (doc *document) links() []*url.URL {
	urls := make([]*url.URL, 0)
	seen := map[string]int{}
	for _, n := range doc.list {
		if n.Type == html.ElementNode && n.Data == "a" {
			v := attrVal(n, "href")
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = 1
			u, err := url.Parse(v)
			if err != nil {
				continue
			}
			if u.Host == "" {
				u.Host = baseURL.Host
			}
			if u.Scheme == "" {
				u.Scheme = baseURL.Scheme
			}
			// check if link is for http
			if !(u.Scheme == "http" || u.Scheme == "https") {
				continue
			}
			//check if link is going to another site
			if u.Host != baseURL.Host {
				continue
			}
			//done ✅
			urls = append(urls, u)
		}
	}
	return urls
}

type product struct {
	url    *url.URL
	Id     string
	OTitle string
	NTitle string
	Images []string
}

func hash(d []byte) string {
	h := sha256.New()
	h.Write(d)
	return hex.EncodeToString(h.Sum(nil)[0:7])
}

func insertProduct(u *url.URL, doc *document) string {
	pid := hash([]byte(u.String()))
	ptitle := doc.title()
	q := `
		insert into products (id, o_title)
		values ($1, $2)
		on conflict on constraint products_pkey
		do update
		set o_title = excluded.o_title
	`
	_, err := db.Exec(q, pid, ptitle)
	check(err)
	imgs := doc.images()
	imgIds := []string{}
	for i := range imgs {
		u, err := url.Parse(imgs[i])
		check(err)
		resp, err := http.Get(u.String())
		if err != nil {
			fmt.Printf("getting image %s %s\n", u.String(), err)
			continue
		}
		orig, err := ioutil.ReadAll(resp.Body)
		check(err)
		imgId := hash(orig)
		sv := s3manager.NewUploader(awsSession)
		_, err = sv.Upload(&s3manager.UploadInput{
			Bucket:      aws.String("imgs.rumblegoods.com"),
			Key:         aws.String(imgId),
			Body:        bytes.NewBuffer(orig),
			ContentType: aws.String(http.DetectContentType(orig)),
		})
		check(err)
		imgIds = append(imgIds, imgId)
	}
	q = `
		insert into images(id, pid)
		values (unnest($1::text[]), $2)
		on conflict on constraint images_pkey do nothing
	`
	_, err = db.Exec(q, pq.StringArray(imgIds), pid)
	check(err)
	return ptitle
}
