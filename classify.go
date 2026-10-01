package main

import (
	"strings"
)

// Identity is how the panel names a container for people: a title, a short
// hint and a kind that decides the wording of warnings.
type Identity struct {
	Title string `json:"title"`
	Sub   string `json:"sub"`
	Kind  string `json:"kind"` // vpn, proxy, self, app, other
}

// Containers created by the Amnezia VPN app always have these names.
var amneziaNames = map[string]Identity{
	"amnezia-awg":           {"AmneziaWG", "VPN", "vpn"},
	"amnezia-awg2":          {"AmneziaWG", "VPN", "vpn"},
	"amnezia-wireguard":     {"WireGuard", "VPN", "vpn"},
	"amnezia-openvpn":       {"OpenVPN", "VPN", "vpn"},
	"amnezia-openvpn-cloak": {"OpenVPN over Cloak", "VPN", "vpn"},
	"amnezia-shadowsocks":   {"OpenVPN over Shadowsocks", "VPN", "vpn"},
	"amnezia-xray":          {"XRay", "VPN", "vpn"},
	"amnezia-ipsec":         {"IKEv2", "VPN", "vpn"},
	"amnezia-dns":           {"AmneziaDNS", "DNS для VPN", "vpn"},
	"amnezia-telemt":        {"Прокси Telegram", "Amnezia", "proxy"},
	"amnezia-socks5proxy":   {"SOCKS5-прокси", "Amnezia", "proxy"},
	"amnezia-tor":           {"Сайт в Tor", "Amnezia", "app"},
	"amnezia-sftp":          {"SFTP", "Amnezia", "app"},
}

// Well-known images, matched by repository name without registry and tag.
var knownImages = map[string]Identity{
	"prichal":                       {"Причал", "панель", "app"},
	"aeyota93-cloud/prichal":        {"Причал", "панель", "app"},
	"n8nio/n8n":                     {"n8n", "автоматизации", "app"},
	"portainer/portainer-ce":        {"Portainer", "управление Docker", "app"},
	"portainer/portainer-ee":        {"Portainer", "управление Docker", "app"},
	"nginx":                         {"Nginx", "веб-сервер", "app"},
	"nginxinc/nginx-unprivileged":   {"Nginx", "веб-сервер", "app"},
	"jc21/nginx-proxy-manager":      {"Nginx Proxy Manager", "веб-сервер", "app"},
	"caddy":                         {"Caddy", "веб-сервер", "app"},
	"traefik":                       {"Traefik", "веб-сервер", "app"},
	"httpd":                         {"Apache", "веб-сервер", "app"},
	"postgres":                      {"PostgreSQL", "база данных", "app"},
	"mysql":                         {"MySQL", "база данных", "app"},
	"mariadb":                       {"MariaDB", "база данных", "app"},
	"mongo":                         {"MongoDB", "база данных", "app"},
	"redis":                         {"Redis", "база данных", "app"},
	"valkey/valkey":                 {"Valkey", "база данных", "app"},
	"louislam/uptime-kuma":          {"Uptime Kuma", "мониторинг", "app"},
	"henrygd/beszel":                {"Beszel", "мониторинг", "app"},
	"henrygd/beszel-agent":          {"Beszel", "агент мониторинга", "app"},
	"grafana/grafana":               {"Grafana", "мониторинг", "app"},
	"prom/prometheus":               {"Prometheus", "мониторинг", "app"},
	"amir20/dozzle":                 {"Dozzle", "журналы", "app"},
	"vaultwarden/server":            {"Vaultwarden", "пароли", "app"},
	"nextcloud":                     {"Nextcloud", "файлы", "app"},
	"gitea/gitea":                   {"Gitea", "git", "app"},
	"containrrr/watchtower":         {"Watchtower", "автообновление", "app"},
	"wg-easy/wg-easy":               {"wg-easy", "VPN", "vpn"},
	"weejewel/wg-easy":              {"wg-easy", "VPN", "vpn"},
	"linuxserver/wireguard":         {"WireGuard", "VPN", "vpn"},
	"mhsanaei/3x-ui":                {"3X-UI", "VPN", "vpn"},
	"adguard/adguardhome":           {"AdGuard Home", "DNS", "app"},
	"pihole/pihole":                 {"Pi-hole", "DNS", "app"},
	"home-assistant/home-assistant": {"Home Assistant", "умный дом", "app"},
}

// imageRepo turns "ghcr.io/wg-easy/wg-easy:15" or "docker.io/library/nginx@sha256:…"
// into "wg-easy/wg-easy" / "nginx": the repository without registry, tag
// and the "library/" of official Docker Hub images.
func imageRepo(image string) string {
	return strings.TrimPrefix(parseRef(image).Repo, "library/")
}

// identify names a container. service is the docker compose service name, if
// any.
func identify(name, image, service string, self bool) Identity {
	if self {
		return Identity{"Причал", "эта панель", "self"}
	}
	if id, ok := amneziaNames[name]; ok {
		return id
	}
	if strings.HasPrefix(name, "amnezia-") {
		return Identity{strings.TrimPrefix(name, "amnezia-"), "Amnezia", "vpn"}
	}
	if id, ok := knownImages[imageRepo(image)]; ok {
		return id
	}
	if service != "" {
		return Identity{service, "", "other"}
	}
	return Identity{name, "", "other"}
}
