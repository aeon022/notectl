#!/bin/bash
set -e

INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
CONFIG_DIR="$HOME/.config/notectl"

echo "Building notectl..."
go build -o "$INSTALL_DIR/notectl" .

# Create config dir and sample config if not present
mkdir -p "$CONFIG_DIR"
if [ ! -f "$CONFIG_DIR/notectl.yaml" ]; then
    cat > "$CONFIG_DIR/notectl.yaml" <<'EOF'
# notectl configuration
# vault_path: ~/Documents/ObsidianVault
EOF
    echo "Created config at $CONFIG_DIR/notectl.yaml"
fi

echo ""
echo "Done! notectl is installed at $INSTALL_DIR/notectl"
echo ""

# Detect vault
if [ -n "$NOTECTL_VAULT_PATH" ]; then
    echo "Vault: $NOTECTL_VAULT_PATH (from env)"
else
    echo "Next: set your vault path in $CONFIG_DIR/notectl.yaml"
    echo "  vault_path: ~/path/to/your/vault"
    echo ""
fi

echo "Then run:"
echo "  notectl sync   # index your notes"
echo "  notectl        # open TUI"
echo ""
echo "For AI integration, add to Claude Desktop config:"
echo '  { "mcpServers": { "notectl": { "command": "notectl", "args": ["mcp"] } } }'
