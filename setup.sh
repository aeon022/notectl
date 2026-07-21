#!/bin/bash
# notectl Setup Utility

# Terminal-Farben definieren
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BOLD='\033[1m'
NC='\033[0m' # Keine Farbe

echo -e "${BLUE}${BOLD}=========================================${NC}"
echo -e "${BLUE}${BOLD}        notectl Setup & Installation      ${NC}"
echo -e "${BLUE}${BOLD}=========================================${NC}"
echo ""

# 1. Go Installation prüfen
echo -e "${BLUE}[1/6] Prüfe Go-Installation...${NC}"
if ! command -v go &> /dev/null; then
    echo -e "${RED}Fehler: Go ist nicht installiert!${NC}"
    echo -e "Bitte installiere Go zuerst über Homebrew:"
    echo -e "  ${YELLOW}brew install go${NC}"
    exit 1
else
    GO_VERSION=$(go version)
    echo -e "${GREEN}✔ Go gefunden:${NC} $GO_VERSION"
fi
echo ""

# 2. Abhängigkeiten herunterladen
echo -e "${BLUE}[2/6] Lade Go-Abhängigkeiten herunter...${NC}"
if go mod download; then
    echo -e "${GREEN}✔ Abhängigkeiten erfolgreich geladen.${NC}"
else
    echo -e "${RED}Fehler beim Herunterladen der Abhängigkeiten!${NC}"
    exit 1
fi
echo ""

# 3. Konfigurationsverzeichnis erstellen
echo -e "${BLUE}[3/6] Bereite Konfiguration vor...${NC}"
CONFIG_DIR="$HOME/.config/notectl"
if [ ! -d "$CONFIG_DIR" ]; then
    mkdir -p "$CONFIG_DIR"
    echo -e "${GREEN}✔ Verzeichnis erstellt:${NC} $CONFIG_DIR"
else
    echo -e "${GREEN}✔ Konfigurationsverzeichnis existiert bereits.${NC}"
fi

if [ ! -f "$CONFIG_DIR/notectl.yaml" ]; then
    cat > "$CONFIG_DIR/notectl.yaml" <<'EOF'
# notectl configuration
# vault_path: ~/Documents/ObsidianVault
# source: obsidian   # obsidian | apple | markdown
EOF
    echo -e "${GREEN}✔ Standard-Konfiguration angelegt:${NC} $CONFIG_DIR/notectl.yaml"
fi
echo ""

# 4. Binary kompilieren & installieren
echo -e "${BLUE}[4/6] Kompiliere und installiere notectl...${NC}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$INSTALL_DIR"
if go build -o "$INSTALL_DIR/notectl" .; then
    echo -e "${GREEN}✔ notectl erfolgreich kompiliert und installiert in:${NC} $INSTALL_DIR/notectl"
else
    echo -e "${RED}Fehler beim Kompilieren von notectl!${NC}"
    exit 1
fi
echo ""

# 5. Apple Notes Kompatibilität & Festplattenvollzugriff (Full Disk Access)
echo -e "${BLUE}[5/6] Apple Notes & Festplattenvollzugriff (Full Disk Access)...${NC}"
if [ -d "$HOME/Library/Group Containers/group.com.apple.notes" ]; then
    if [ -r "$HOME/Library/Group Containers/group.com.apple.notes/NoteStore.sqlite" ]; then
        echo -e "${GREEN}✔ Festplattenvollzugriff erkannt:${NC} NoteStore.sqlite ist lesbar."
    else
        echo -e "${YELLOW}⚠️  Hinweis zum Festplattenvollzugriff (Full Disk Access):${NC}"
        echo -e "   Wenn du Apple Notes ('source: apple') nutzt, liefert macOS AppleScript abgehakte"
        echo -e "   GUI-Checklisten standardmäßig ohne Checkbox-Status aus (☑ vs ☐)."
        echo -e "   Damit native Checkboxen korrekt synchronisiert werden, benötigt deine Terminal-App Vollzugriff."
        echo ""
        if [ -t 0 ]; then
            read -p "Möchtest du die Systemeinstellungen (Datenschutz & Sicherheit -> Festplattenvollzugriff) jetzt öffnen? (y/n): " -n 1 -r response
            echo ""
            case "$response" in
                [yY][eE][sS]|[yY])
                    echo -e "${BLUE}Öffne Systemeinstellungen -> Datenschutz & Sicherheit -> Festplattenvollzugriff...${NC}"
                    open "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"
                    echo -e "${YELLOW}👉 Bitte aktiviere dein Terminal in der Liste und starte das Terminal danach neu.${NC}"
                    ;;
                *)
                    echo -e "${YELLOW}Übersprungen.${NC} Du kannst es jederzeit selbst in den Systemeinstellungen aktivieren oder via:"
                    echo -e "  ${BOLD}open \"x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles\"${NC}"
                    ;;
            esac
        else
            echo -e "   Um Checkboxen korrekt zu synchronisieren, öffne Systemeinstellungen -> Datenschutz & Sicherheit -> Festplattenvollzugriff"
            echo -e "   oder führe aus: ${BOLD}open \"x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles\"${NC}"
        fi
    fi
else
    echo -e "${GREEN}✔ Kein lokales Apple Notes Verzeichnis erkannt (Obsidian/Markdown-Modus aktiv).${NC}"
fi
echo ""

# 6. KI-Integration (MCP Server für Claude Desktop, Antigravity agy & Codex)
echo -e "${BLUE}[6/6] KI-Integration (MCP Server Konfiguration)...${NC}"
if [ -t 0 ]; then
    read -p "Möchtest du notectl automatisch bei deinen KI-Tools (Claude, agy, codex) eintragen? (y/n): " -n 1 -r response
    echo ""
    case "$response" in
        [yY][eE][sS]|[yY])
            AUTO_MCP=true
            ;;
        *)
            AUTO_MCP=false
            ;;
    esac
else
    AUTO_MCP=true
fi

if [ "$AUTO_MCP" = true ]; then
    python3 -c '
import json, os, sys, re

cmd = sys.argv[1]
updated = 0

# 1. Claude Desktop
claude_dir = os.path.expanduser("~/Library/Application Support/Claude")
if os.path.exists(claude_dir):
    claude_path = os.path.join(claude_dir, "claude_desktop_config.json")
    data = {}
    if os.path.exists(claude_path):
        try:
            with open(claude_path, "r", encoding="utf-8") as f:
                data = json.load(f)
        except Exception:
            pass
    if "mcpServers" not in data or not isinstance(data["mcpServers"], dict):
        data["mcpServers"] = {}
    data["mcpServers"]["notectl"] = {"command": cmd, "args": ["mcp"]}
    try:
        with open(claude_path, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=2)
        print("  ✔ Claude Desktop: claude_desktop_config.json")
        updated += 1
    except Exception as e:
        print(f"  ❌ Claude Desktop Fehler: {e}")

# 2. Google Antigravity (agy / Antigravity IDE)
gemini_dir = os.path.expanduser("~/.gemini/config")
if os.path.exists(gemini_dir):
    gemini_path = os.path.join(gemini_dir, "mcp_config.json")
    data = {}
    if os.path.exists(gemini_path):
        try:
            with open(gemini_path, "r", encoding="utf-8") as f:
                data = json.load(f)
        except Exception:
            pass
    if "mcpServers" not in data or not isinstance(data["mcpServers"], dict):
        data["mcpServers"] = {}
    data["mcpServers"]["notectl"] = {"command": cmd, "args": ["mcp"]}
    try:
        with open(gemini_path, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=2)
        print("  ✔ Google Antigravity (agy): ~/.gemini/config/mcp_config.json")
        updated += 1
    except Exception as e:
        print(f"  ❌ Google Antigravity Fehler: {e}")

# 3. OpenAI Codex CLI (codex)
codex_dir = os.path.expanduser("~/.codex")
if os.path.exists(codex_dir):
    codex_path = os.path.join(codex_dir, "config.toml")
    content = ""
    if os.path.exists(codex_path):
        try:
            with open(codex_path, "r", encoding="utf-8") as f:
                content = f.read()
        except Exception:
            pass
    if "[mcp_servers.notectl]" not in content:
        try:
            with open(codex_path, "a", encoding="utf-8") as f:
                if content and not content.endswith("\n"):
                    f.write("\n")
                f.write(f"\n[mcp_servers.notectl]\ncommand = \"{cmd}\"\nargs = [\"mcp\"]\n")
            print("  ✔ OpenAI Codex: ~/.codex/config.toml")
            updated += 1
        except Exception as e:
            print(f"  ❌ OpenAI Codex Fehler: {e}")
    else:
        print("  ✔ OpenAI Codex: ~/.codex/config.toml (bereits eingetragen)")
        updated += 1

if updated == 0:
    print("  ℹ Keine bekannten KI-Konfigurationsordner gefunden.")
' "$INSTALL_DIR/notectl"
    echo -e "${GREEN}✔ MCP-Server erfolgreich bei deinen aktiven KI-Tools konfiguriert!${NC}"
    echo -e "   (Bitte starte offene KI-Apps wie Claude Desktop nach dem Setup neu.)"
else
    echo -e "${YELLOW}Übersprungen.${NC} Du kannst den MCP-Server später manuell eintragen (Befehl: ${BOLD}$INSTALL_DIR/notectl mcp${NC})."
fi
echo ""

# Nächste Schritte & Zusammenfassung
echo -e "${BLUE}${BOLD}=========================================${NC}"
echo -e "${GREEN}${BOLD}✔ Installation abgeschlossen!${NC}"
echo -e "${BLUE}${BOLD}=========================================${NC}"
echo ""
echo -e "So geht es weiter:"
echo -e "  1. Konfiguriere deinen Vault in: ${YELLOW}$CONFIG_DIR/notectl.yaml${NC}"
echo -e "  2. Notizen indizieren:           ${GREEN}${BOLD}notectl sync${NC}"
echo -e "  3. TUI starten:                  ${GREEN}${BOLD}notectl${NC}"
echo ""

