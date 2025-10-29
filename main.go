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
	"sync"
	"time"
)

type ClipItem struct {
	Text      string    `json:"text"`
	UpdatedAt time.Time `json:"updatedAt"`
}

var (
	lastItem     = ClipItem{}
	clients      = make(map[chan []byte]bool)
	broadcast    = make(chan []byte, 256)
	serverPwd    string
	clientsMutex sync.Mutex
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
	// Start the broadcast goroutine
	go func() {
		for message := range broadcast {
			clientsMutex.Lock()
			for clientCh := range clients {
				// Send the message to each client's channel, but don't block if channel is full
				select {
				case clientCh <- message:
					// Message sent successfully
				default:
					log.Printf("Broadcast: Client channel full, message dropped")
				}
			}
			clientsMutex.Unlock()
		}
	}()

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
		log.Printf("AUTH: Browser auth check for %s accessing %s", r.RemoteAddr, r.URL.Path)
		if !isAuthorized(r) {
			log.Printf("AUTH: Browser auth failed for %s, redirecting to login", r.RemoteAddr)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		log.Printf("AUTH: Browser auth succeeded for %s accessing %s", r.RemoteAddr, r.URL.Path)
		next(w, r)
	}
}

func apiAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("AUTH: API auth check for %s accessing %s", r.RemoteAddr, r.URL.Path)
		if !isAuthorized(r) && !headerAuth(r) {
			log.Printf("AUTH: API auth failed for %s accessing %s", r.RemoteAddr, r.URL.Path)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		log.Printf("AUTH: API auth succeeded for %s accessing %s", r.RemoteAddr, r.URL.Path)
		next(w, r)
	}
}

func isAuthorized(r *http.Request) bool {
	c, err := r.Cookie("auth")
	if err != nil {
		log.Printf("AUTH: No auth cookie found for %s: %v", r.RemoteAddr, err)
		return false
	}
	authValid := c.Value == "1"
	log.Printf("AUTH: Cookie auth check for %s: %v", r.RemoteAddr, authValid)
	return authValid
}

func headerAuth(r *http.Request) bool {
	headerAuth := r.Header.Get("X-Auth") == serverPwd
	log.Printf("AUTH: Header auth check for %s: %v", r.RemoteAddr, headerAuth)
	return headerAuth
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
	log.Printf("SET: Received set request from %s", r.RemoteAddr)
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("SET: Failed to decode request body from %s: %v", r.RemoteAddr, err)
		http.Error(w, "bad request", 400)
		return
	}
	log.Printf("SET: Decoded body text from %s: %s", r.RemoteAddr, body.Text)
	text := strings.TrimSpace(body.Text)
	if text == "" {
		log.Printf("SET: Empty text received from %s", r.RemoteAddr)
		http.Error(w, "empty text", 400)
		return
	}
	lastItem = ClipItem{Text: text, UpdatedAt: time.Now()}
	log.Printf("SET: Updated lastItem with text: %s", text)
	data, _ := json.Marshal(lastItem)
	log.Printf("SET: Prepared JSON data to send to SSE channel: %s", string(data))

	// Send the message to the broadcast channel for all clients
	select {
	case broadcast <- data:
		log.Printf("SET: Successfully sent data to broadcast channel from %s", r.RemoteAddr)
	default:
		log.Printf("SET: Failed to send data to broadcast channel (channel full) from %s", r.RemoteAddr)
	}

	w.WriteHeader(204)
	log.Printf("SET: Sent 204 response to %s", r.RemoteAddr)
}

func eventsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log.Printf("EVENTS: New SSE connection request from %s", r.RemoteAddr)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("EVENTS: SSE unsupported by client %s", r.RemoteAddr)
		http.Error(w, "SSE unsupported", 500)
		return
	}

	// Create a unique channel for this client
	clientCh := make(chan []byte, 256)

	// Register this client in the global clients map
	clientsMutex.Lock()
	clients[clientCh] = true
	clientsMutex.Unlock()

	// Send the current clipboard content to the new client if available
	if lastItem.Text != "" {
		data, _ := json.Marshal(lastItem)
		select {
		case clientCh <- data:
			log.Printf("EVENTS: Sent current clipboard content to new client %s", r.RemoteAddr)
		default:
			log.Printf("EVENTS: Failed to send current clipboard content to new client %s", r.RemoteAddr)
		}
	}

	flusher.Flush()
	log.Printf("EVENTS: SSE headers set and flushed for client %s", r.RemoteAddr)

	// Start a goroutine to handle sending messages to this client
	go func() {
		log.Printf("EVENTS: Starting SSE message relay goroutine for client %s", r.RemoteAddr)
		for {
			select {
			case msg := <-clientCh:
				log.Printf("EVENTS: Sending message to client %s: %s", r.RemoteAddr, string(msg))
				fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
				log.Printf("EVENTS: Message flushed to client %s", r.RemoteAddr)
			case <-ctx.Done():
				log.Printf("EVENTS: Context cancelled for client %s, connection closed", r.RemoteAddr)
				// Unregister client when connection is closed
				clientsMutex.Lock()
				delete(clients, clientCh)
				clientsMutex.Unlock()
				log.Printf("EVENTS: Client %s unregistered from global client list", r.RemoteAddr)
				return
			}
		}
	}()

	// Keep the connection alive and listen for broadcast messages
	for {
		select {
		case <-ctx.Done():
			log.Printf("EVENTS: Context cancelled for client %s", r.RemoteAddr)
			return
		}
	}
}

// ======================= CLIENT =======================

func runClient(server, password string) {
	log.Printf("CLIENT: Starting client to connect to server %s", server)
	go func() {
		log.Printf("CLIENT: Starting SSE listener goroutine for server %s", server)
		for {
			log.Printf("CLIENT: Attempting to connect to SSE endpoint at %s", server)
			err := sseListen(server, password)
			log.Printf("CLIENT: SSE connection ended for %s with error: %v", server, err)
			log.Printf("CLIENT: Waiting 2 seconds before reconnecting to %s", server)
			time.Sleep(2 * time.Second)
		}
	}()

	var last string
	log.Printf("CLIENT: Starting clipboard monitoring loop for server %s", server)
	for {
		log.Printf("CLIENT: Checking clipboard content...")
		txt, err := getClipboardText()
		if err != nil {
			log.Printf("CLIENT: Error getting clipboard text: %v", err)
		} else {
			log.Printf("CLIENT: Retrieved clipboard text: %s (length: %d)", txt, len(txt))
			if txt != "" && txt != last {
				log.Printf("CLIENT: Clipboard content changed from '%s' to '%s'", last, txt)
				body, _ := json.Marshal(map[string]string{"text": txt})
				req, _ := http.NewRequest("POST", server+"/api/set", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Auth", password)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					log.Printf("CLIENT: Failed to send clipboard to server %s: %v", server, err)
				} else {
					log.Printf("CLIENT: Sent clipboard to server %s, response status: %d", server, resp.StatusCode)
					resp.Body.Close()
				}
				last = txt
				log.Printf("CLIENT: Updated last clipboard value to: %s", txt)
			}
		}
		time.Sleep(800 * time.Millisecond)
	}
}

func sseListen(server, password string) error {
	log.Printf("SSE: Initiating SSE connection to %s", server)
	req, _ := http.NewRequest("GET", server+"/events", nil)
	req.Header.Set("X-Auth", password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("SSE: Failed to connect to server %s: %v", server, err)
		return err
	}
	log.Printf("SSE: Connected to server %s, status: %d", server, resp.StatusCode)
	defer func() {
		log.Printf("SSE: Closing connection to server %s", server)
		resp.Body.Close()
	}()

	buf := make([]byte, 4096)
	for {
		log.Printf("SSE: Reading data from server %s", server)
		n, err := resp.Body.Read(buf)
		if n > 0 {
			log.Printf("SSE: Received %d bytes from server %s: %s", n, server, string(buf[:n]))
			part := buf[:n]
			for _, line := range strings.Split(string(part), "\n") {
				if strings.HasPrefix(line, "data:") {
					log.Printf("SSE: Processing data line: %s", line)
					var it ClipItem
					if json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &it) == nil {
						log.Printf("SSE: Successfully parsed ClipItem: %s", it.Text)
						setClipboardText(it.Text)
						log.Printf("SSE: Set clipboard with received text: %s", it.Text)
					} else {
						log.Printf("SSE: Failed to unmarshal JSON: %s", strings.TrimSpace(line[5:]))
					}
				}
			}
		}
		if err != nil {
			log.Printf("SSE: Error reading from server %s: %v", server, err)
			return err
		}
	}
}

// ======================= CLIPBOARD =======================

func getClipboardText() (string, error) {
	log.Printf("CLIPBOARD: Attempting to get clipboard text")
	cmds := [][]string{
		{"wl-paste", "-n"},
		{"xclip", "-o", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--output"},
	}
	for i, c := range cmds {
		log.Printf("CLIPBOARD: Trying clipboard command %d: %s", i+1, strings.Join(c, " "))
		out, err := exec.Command(c[0], c[1:]...).Output()
		if err == nil {
			result := string(out)
			log.Printf("CLIPBOARD: Successfully retrieved clipboard text: %s (length: %d)", result, len(result))
			return result, nil
		} else {
			log.Printf("CLIPBOARD: Command %d failed: %s with error: %v", i+1, strings.Join(c, " "), err)
		}
	}
	log.Printf("CLIPBOARD: All clipboard commands failed, returning error")
	return "", fmt.Errorf("no clipboard tool found")
}

func setClipboardText(s string) error {
	log.Printf("CLIPBOARD: Attempting to set clipboard text: %s (length: %d)", s, len(s))
	cmds := [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	}
	for i, c := range cmds {
		log.Printf("CLIPBOARD: Trying clipboard command %d: %s", i+1, strings.Join(c, " "))
		cmd := exec.Command(c[0], c[1:]...)
		stdin, _ := cmd.StdinPipe()
		go func() {
			io.WriteString(stdin, s)
			stdin.Close()
			log.Printf("CLIPBOARD: Wrote text to stdin for command %d", i+1)
		}()
		if err := cmd.Run(); err == nil {
			log.Printf("CLIPBOARD: Successfully set clipboard using command %d: %s", i+1, strings.Join(c, " "))
			return nil
		} else {
			log.Printf("CLIPBOARD: Command %d failed: %s with error: %v", i+1, strings.Join(c, " "), err)
		}
	}
	log.Printf("CLIPBOARD: All clipboard commands failed to set text: %s", s)
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
