package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

type ClipItem struct {
	Text      string    `json:"text"`
	UpdatedAt time.Time `json:"updatedAt"`
}

var (
	lastItem  = ClipItem{}
	sseCh     = make(chan []byte, 16)
	serverPwd string
)

func main() {
	serverPwd = os.Getenv("CLIPSYNC_PASSWORD")
	if serverPwd == "" {
		log.Fatal("❌ CLIPSYNC_PASSWORD environment variable not set")
	}

	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "server":
		addr := ":9000"
		if len(os.Args) > 2 {
			addr = os.Args[2]
		}
		runServer(addr)
	case "client":
		if len(os.Args) < 3 {
			usage()
			return
		}
		runClient(os.Args[2], serverPwd)
	default:
		usage()
	}
}

func usage() {
	fmt.Println(`Usage:
  CLIPSYNC_PASSWORD=yourpass clipsync server :9000
  clipsync client http://<server>:9000`)
}

// ======================= SERVER =======================

func runServer(addr string) {
	mux := http.NewServeMux()

	mux.HandleFunc("/login", loginHandler)
	mux.HandleFunc("/logout", logoutHandler)

	mux.HandleFunc("/", browserAuth(indexHandler))
	mux.HandleFunc("/api/get", apiAuth(getHandler))
	mux.HandleFunc("/api/set", apiAuth(setHandler))
	mux.HandleFunc("/events", apiAuth(eventsHandler))

	log.Printf("🔐 Server running at %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func browserAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAuthorized(r) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func apiAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAuthorized(r) && !headerAuth(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func isAuthorized(r *http.Request) bool {
	c, err := r.Cookie("auth")
	return err == nil && c.Value == "1"
}

func headerAuth(r *http.Request) bool {
	return r.Header.Get("X-Auth") == serverPwd
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		if r.FormValue("password") == serverPwd {
			http.SetCookie(w, &http.Cookie{
				Name:     "auth",
				Value:    "1",
				Path:     "/",
				HttpOnly: true,
				MaxAge:   86400,
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		http.Error(w, "Invalid password", 401)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, loginPage)
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:   "auth",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pageTmpl.Execute(w, nil)
}

func getHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(lastItem)
}

func setHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		http.Error(w, "empty text", 400)
		return
	}
	lastItem = ClipItem{Text: text, UpdatedAt: time.Now()}
	data, _ := json.Marshal(lastItem)
	sseCh <- data
	w.WriteHeader(204)
}

func eventsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE unsupported", 500)
		return
	}
	flusher.Flush()
	clientCh := make(chan []byte, 4)
	go func() {
		for msg := range sseCh {
			select {
			case clientCh <- msg:
			default:
			}
		}
	}()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-clientCh:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

// ======================= CLIENT =======================

func runClient(server, password string) {
	go func() {
		for {
			err := sseListen(server, password)
			log.Println("SSE error:", err)
			time.Sleep(2 * time.Second)
		}
	}()

	var last string
	for {
		txt, err := getClipboardText()
		if err == nil && txt != "" && txt != last {
			body, _ := json.Marshal(map[string]string{"text": txt})
			req, _ := http.NewRequest("POST", server+"/api/set", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Auth", password)
			http.DefaultClient.Do(req)
			last = txt
			log.Println("Sent clipboard:", txt)
		}
		time.Sleep(800 * time.Millisecond)
	}
}

func sseListen(server, password string) error {
	req, _ := http.NewRequest("GET", server+"/events", nil)
	req.Header.Set("X-Auth", password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			part := buf[:n]
			for _, line := range strings.Split(string(part), "\n") {
				if strings.HasPrefix(line, "data:") {
					var it ClipItem
					if json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &it) == nil {
						setClipboardText(it.Text)
						log.Println("Received clipboard:", it.Text)
					}
				}
			}
		}
		if err != nil {
			return err
		}
	}
}

// ======================= CLIPBOARD =======================

func getClipboardText() (string, error) {
	cmds := [][]string{
		{"wl-paste", "-n"},
		{"xclip", "-o", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--output"},
	}
	for _, c := range cmds {
		out, err := exec.Command(c[0], c[1:]...).Output()
		if err == nil {
			return string(out), nil
		}
	}
	return "", fmt.Errorf("no clipboard tool found")
}

func setClipboardText(s string) error {
	cmds := [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		stdin, _ := cmd.StdinPipe()
		go func() {
			io.WriteString(stdin, s)
			stdin.Close()
		}()
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no clipboard backend found")
}

// ======================= WEB PAGES =======================

const loginPage = `
<!doctype html>
<html>
	<head>
	<meta charset="utf-8"/>
	<meta name="viewport" content="width=device-width,initial-scale=1"/>
	<meta name="theme-color" content="#000000">
	<meta name="apple-mobile-web-app-capable" content="yes">
	<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">
<title>ClipSync Login</title>
<style>
body{font-family:'Fira Code',monospace;background:#000;color:#0f0;display:flex;align-items:center;justify-content:center;height:100vh;}
form{background:#000;border:1px solid #0f0;padding:20px;border-radius:10px;box-shadow:0 0 15px #0f0;}
input,button{font-family:'Fira Code',monospace;background:#000;color:#0f0;border:1px solid #0f0;border-radius:5px;padding:10px;margin-top:5px}
button:hover{background:#0f0;color:#000;cursor:pointer}
h3{margin-bottom:10px}
</style></head>
<body>
<form method="post">
<h3>🔐 ClipSync Login</h3>
<input type="password" name="password" placeholder="Password" required autofocus>
<button type="submit">Login</button>
</form>
</body></html>
`

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<meta name="theme-color" content="#000000">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">
<title>ClipSync Terminal</title>
<style>
body{background:#000;color:#0f0;font-family:'Fira Code',monospace;margin:0;padding:20px;}
h2{color:#0f0;}
	button{width: 100%;font-family:'Fira Code',monospace;background:#000;color:#0f0;border:1px solid #0f0;border-radius:5px;padding:8px 14px;margin-top:6px}
button:hover{background:#0f0;color:#000;cursor:pointer}
pre{white-space:pre-wrap;word-break:break-word;background:#000;padding:10px;border:1px solid #0f0;border-radius:5px;box-shadow:0 0 8px #0f0;}
a{color:#0f0;text-decoration:none}
a:hover{text-decoration:underline}
.blink{animation:blink 1s steps(2,start) infinite}
@keyframes blink{to{visibility:hidden}}
</style>
</head>
<body>
<div style="display:flex;justify-content:space-between;align-items:center;">
<h2>🖥️ ClipSync <span class="blink">█</span></h2>
<a href="/logout"><button>logout</button></a>
</div>

	<div style="margin:15px 0; display: flex; justify-content: space-between; gap: 20px;">
<button onclick="pasteFromClipboard()">📥 Paste from Clipboard</button>
<button onclick="copyCurrent()">📋 Copy current text</button>
</div>

<h3>Current clipboard:</h3>
<pre id="current">(empty)</pre>

<script>
async function pasteFromClipboard(){
  try{
    let text="";
    if(navigator.clipboard && navigator.clipboard.readText){
      text=await navigator.clipboard.readText();
    }else{
      text=prompt("Clipboard API unavailable.\\nPaste text manually:");
      if(text){
        // update UI immediately for prompt-based paste
        document.getElementById("current").textContent=text;
      }
    }
    if(!text)return;
    await fetch("/api/set",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({text})});
    document.getElementById("current").textContent=text;
  }catch(e){alert("Clipboard read failed: "+e);}
}

async function copyCurrent(){
  const t=document.getElementById("current").textContent;
  if(!t)return;
  try{
    if(navigator.clipboard && navigator.clipboard.writeText){
      await navigator.clipboard.writeText(t);
    }else{
      const area=document.createElement("textarea");
      area.value=t;document.body.appendChild(area);area.select();
      document.execCommand("copy");
      document.body.removeChild(area);
    }
    alert("Copied to clipboard!");
  }catch(e){alert("Copy failed: "+e);}
}

async function getLatest(){
  const r=await fetch("/api/get");const d=await r.json();
  document.getElementById("current").textContent=d.text||"(empty)";
}
function connectSSE(){
  const es=new EventSource("/events");
  es.onmessage=(e)=>{try{const d=JSON.parse(e.data);
    document.getElementById("current").textContent=d.text;}catch(_){}}; 
  es.onerror=()=>setTimeout(connectSSE,2000);
}
getLatest();connectSSE();
</script>
</body>
</html>`))
