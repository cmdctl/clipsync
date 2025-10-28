# ClipSync

ClipSync is a lightweight clipboard synchronization tool that allows you to share clipboard content across multiple devices over a network. It works by running a server on one device and clients on other devices, enabling seamless clipboard sharing.

## Features

- Cross-device clipboard synchronization
- Web-based UI for server control
- Secure authentication with password protection
- Real-time updates using Server-Sent Events (SSE)
- Support for Linux clipboard tools (wl-clipboard, xclip, xsel)

## Installation

First, ensure you have Go installed on your system. Then, install ClipSync:

```bash
go install github.com/cmdctl/golang/clipsync@latest
```

Or clone the repository and build it:

```bash
git clone https://github.com/cmdctl/golang/clipsync.git
cd clipsync
go build
```

## Usage

### Setup

ClipSync requires a password for authentication. Set the `CLIPSYNC_PASSWORD` environment variable or include it in your command:

```bash
export CLIPSYNC_PASSWORD=your_secure_password
```

### Server Mode

Run the server on the device that will act as the central clipboard hub:

```bash
CLIPSYNC_PASSWORD=yourpass clipsync server :9000
```

This starts a web server at `http://localhost:9000` with a web interface to manage clipboard content.

### Client Mode

Run the client on devices that will synchronize their clipboard with the server:

```bash
clipsync client http://<server-ip>:9000
```

Replace `<server-ip>` with the actual IP address of the server device.

## How It Works

- The server runs a web interface and REST API to manage clipboard content
- Clients continuously monitor their local clipboard and send updates to the server
- The server broadcasts clipboard changes to all clients using Server-Sent Events
- Clients apply incoming clipboard updates to their local clipboard
- Authentication is handled via password for both web UI and API access

## Web Interface

The server provides a web interface accessible at `http://<server-ip>:9000`:

- Login with your password
- View the current synchronized clipboard content
- Manually paste from or copy to the clipboard through the web interface

## Security

- All communication requires authentication with the shared password
- Password is required in both server and client environments
- API endpoints are protected from unauthorized access
- Web interface session uses HTTP cookies with HttpOnly flag

## Compatibility

ClipSync supports various Linux clipboard backends:
- Wayland: `wl-clipboard` (wl-copy, wl-paste)
- X11: `xclip` or `xsel`

Make sure to have at least one of these tools installed for clipboard functionality.

## Configuration

### Port Configuration

Run the server on a different port by specifying it as an argument:

```bash
CLIPSYNC_PASSWORD=yourpass clipsync server :8080
```

### Environment Variables

- `CLIPSYNC_PASSWORD`: Required password for authentication

## Troubleshooting

1. **Clipboard not working**: Ensure you have one of the supported clipboard tools installed (wl-clipboard, xclip, xsel)

2. **Connection issues**: Verify that the server IP address and port are accessible from client devices

3. **Authentication errors**: Make sure the same password is used for both server and client

4. **Real-time updates not working**: Check that your firewall allows connections on the specified port

## License

This project is licensed under the terms specified in the repository.