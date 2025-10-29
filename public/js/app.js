async function pasteFromClipboard() {
  try {
    let text = "";
    if (navigator.clipboard && navigator.clipboard.readText) {
      text = await navigator.clipboard.readText();
    } else {
      text = prompt("Clipboard API unavailable.\nPaste text manually:");
      if (text) {
        // update UI immediately for prompt-based paste
        document.getElementById("current").textContent = text;
      }
    }
    if (!text) return;
    await fetch("/api/set", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text }),
    });
    document.getElementById("current").textContent = text;
  } catch (e) {
    showToast("Clipboard read failed: " + e);
  }
}

async function copyCurrent() {
  const el = document.getElementById("current");
  if (!el) return console.error("❌ Element with id='current' not found");

  const text = el.textContent?.trim();
  if (!text) return console.warn("⚠️ Nothing to copy");

  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
    } else {
      const area = document.createElement("textarea");
      area.value = text;
      area.style.position = "fixed"; // prevent scrolling
      area.style.opacity = "0";
      document.body.appendChild(area);
      area.focus();
      area.select();
      document.execCommand("copy");
      document.body.removeChild(area);
    }

    // Optional: non-blocking visual feedback
    console.log("✅ Copied to clipboard!");
    showToast("Copied!");
  } catch (err) {
    console.error("❌ Copy failed:", err);
    showToast("Copy failed");
  }
}

// Simple toast message (optional)
function showToast(msg) {
  const toast = document.createElement("div");
  toast.textContent = msg;
  Object.assign(toast.style, {
    position: "fixed",
    left: "50%",
    bottom: "20px",
    transform: "translateX(-50%)",
    maxWidth: "90%", // prevents overflow on small screens
    background: "#0f0",
    color: "#000",
    border: "1px solid #0f0",
    padding: "10px 16px",
    borderRadius: "6px",
    textAlign: "center",
    opacity: "0",
    transition: "opacity 0.3s",
    zIndex: "9999",
    boxSizing: "border-box",
  });
  document.body.appendChild(toast);
  requestAnimationFrame(() => (toast.style.opacity = "1"));
  setTimeout(() => {
    toast.style.opacity = "0";
    setTimeout(() => toast.remove(), 300);
  }, 3000);
}

async function getLatest() {
  const r = await fetch("/api/get");
  const d = await r.json();
  document.getElementById("current").textContent = d.text || "(empty)";
}
function connectSSE() {
  const es = new EventSource("/events");
  es.onmessage = (e) => {
    try {
      const d = JSON.parse(e.data);
      document.getElementById("current").textContent = d.text;
    } catch (_) {}
  };
  es.onerror = () => setTimeout(connectSSE, 2000);
}
getLatest();
connectSSE();

