# Comprehensive Free VPN Blocklist

> **Purpose:** DNS‑block the known domains, CDN endpoints, API servers, and mirror/phishing domains used by free circumvention VPNs.
>
> **Targets:** X‑VPN (Xvpn), Psiphon, Lantern, Hotspot Shield, plus the Pango Group family (Betternet, Touch VPN, VPN 360, Ultra VPN) and other common free VPNs.
>
> **Maintenance:** This list should be reviewed and updated monthly — free VPNs rotate domains frequently.
>
> **Source:** Compiled from network research (Netify), CT log analysis, the `school-vpn-blocking-implementation.md` guide, and known threat intelligence feeds.

---

## Format

Each section is a plain list of **domains** suitable for:
- DNS‑level blocking (Pi‑hole, Unbound, AdGuard Home)
- Firewall URL filtering (pfSense, opnSense)
- Browser / Chromebook URL blocklists (Google Admin Console)
- `/etc/hosts` entries (all pointing to `0.0.0.0` or `127.0.0.1`)
- Proxy / Squid domain denylists

Add `*.domain.tld` (wildcard) wherever your DNS filter supports it.

---

## 1. X‑VPN (Xvpn) — Primary & Mirror Domains

X‑VPN uses an aggressive domain‑rotation strategy with dozens of lookalike/mirror domains for its download pages, API endpoints, and CDN.

### Official & CDN
```
xvpn.io
www.xvpn.io
api.xvpn.io
cdn.xvpn.io
m.xvpn.io
help.xvpn.io
help-center.xvpn.io
```

### Common Mirror / Phishing / Download Domains
```
xvpn-web.com
getxvpn.com
getxvpn.club
getxvpn.services
getxvpn.website
getxvpn.xyz
getvpn-x.com
getvpnx.com
getxweb.com
getxhelp.com
get-x-official-website.com
get-x-officialweb.com
get-x-officialwebsite.com
get-x-vip-website.com
get-x-vpn-web.com
get-x-web-link1.com
get-x-web-link2.com
get-x-web1-link.com
get-x-web2-link.com
get-x-website.com
getx-x-mirror.com
getxappleapilinks.com
get-xmore-link3s.com
get-xmore-links8.com
getxweb-help-link3.com
getxweb-help-link4.com
getxvpn.website
websitevpnx.com
x-web-vip.com
vip-xvpn.com
gopremium-xvpn.com
```

### X‑VPN App Store / Marketing
```
xvpn.en.softonic.com
apps.microsoft.com/detail/9pkl3h9lwmb7
chromewebstore.google.com/detail/x-vpn-free-vpn-chrome-ext/flaeifplnkmoagonpbjmedjcadegiigl
```

### X‑VPN Dynamic / Fast‑Flux Endpoints
These auto‑generated domains are tied to X‑VPN's infrastructure and API backends:
```
37er19htfd.execute-api.us-east-1.amazonaws.com
46sdzf3zdg1dxg2.us
482ec07ef75b3d4e8801348f4dae4ddd9fb6e0d0.com
5wv8mwgztv.com
75a91dc18254371ceef7451db7f54a1d67f6833a.com
8v9m.com
amwmhhqbtu.com
analytics-gooal.com
api-aqqle-link3.com
api-lyndo.com
api.catch.gift
arcai.com
clickwork7secure.com
coloredwool.us
contentabc.com
d32z5ni8t5127x.cloudfront.net
dialertoserver.com
domainownership.us
duapps.com
ec2-34-194-122-51.compute-1.amazonaws.com
ew32rgfdls.com
favoriteshoes.us
find-the-x-web2.com
find-x-web2.com
findxweblinks3.com
floating-rate.us
frandcomfort.us
get-dostluk-lk.com
get-outlook-lk3.com
get-reddit-x-web.com
get-web-x-link90.com
get-web-x-link95.com
gifttrees.us
granules.us
jkfdslkhfzkj.tk
jqja6tr.com
judua3rtinpst0s.xyz
kmmasxeysu.com
leekemx.com
legaladvisor.us
ltlxvxjjmvhn.me
market-id-auto.com
master-clock.us
metal-masters.us
metalinjection.us
mobrain.xyz
monsmousmada.tk
mrr933q4t.com
muchgates97.life
news-iisbangalore-lk2.com
newton.toolspire.com
nur68cnnc5.com
one-zs-lk3.com
onlineload-kja.live
personaly.click
play5r.com
printruns.us
printtools.us
pushokey.com
quora-x2.com
ratecuts.us
restaurantgift.us
rrywkdewtr.com
rwmhczb3363.com
sandparticles.us
school-websites.us
schoolnewsletter.us
selfadserver.com
shelljacket.us
shopping-website.us
shrunkunseeingbacklight.info
smpbhfiwr.com
socialnetworkinfo.us
sporabemer.tk
ssl-mzstatic.com
starruby.us
staticnetcontent.com
sunnetdata.com
suppheathcbelgperg.tk
tbunet.com
trlone.com
u8sj3.com
ubpcgqbrvu.com
umperrdmmq.com
videocollage.us
vornz.com
vpn-servers-lb-1273940223.us-east-1.elb.amazonaws.com
waitfree.us
worldgravityapp.com
xdzyjmfgqj.com
xghfi97mk6.com
xvideos-cdn.com
yxca6625zeyff.com
zcoup.com
zdenorkorust.tk
zmksxksaqh.com
zqe2jx29qa.com
ads-help-link2.com
warninglog.com
blogs-analysis.com
india-timeline.com
crashes-analysis.com
uinlive-time.com
adb-dns.com
turkmen-helper.com
cars-a.co.za
newads-cdn.com
indialifes.com
```

---

## 2. Psiphon

Psiphon uses multiple domains for its tunnel-core clients, server lists, MEEK domain fronting, and download portals.

### Primary Domains
```
psiphon.ca
www.psiphon.ca
psiphon.ca.com
psiphon.ca.net
cdn.psiphon.ca
api.psiphon.ca
download.psiphon.ca
```

### Psiphon 3 (client / distribution)
```
psiphon3.com
www.psiphon3.com
psiphon3.net
www.psiphon3.net
```

### Other Psiphon Properties
```
psiphoneverywhere.com
www.psiphoneverywhere.com
psiphoncdn.com
psiphon.net
```

### Psiphon on Third‑Party App Stores
```
psiphon.en.softonic.com
psiphon.softonic.com
```

### Known Psiphon Server‑List Hosts
These domains have historically hosted Psiphon's `server_list` / `servers.dat`:
```
github.com/Psiphon-Labs/psiphon-tunnel-core
github.com/thispc/psiphon
github.com/KokoseiJ/psiphon
```

---

## 3. Lantern (Getlantern)

Lantern uses its main domains plus S3 buckets and CDN endpoints for distribution and updates.

### Primary Domains
```
getlantern.org
www.getlantern.org
lantern.io
www.lantern.io
lantern.net
www.lantern.net
```

### Lantern Distribution / CDN
```
s3.amazonaws.com/getlantern.org
beta.getlantern.org.s3-website-us-east-1.amazonaws.com
getlantern.org.s3-website-us-east-1.amazonaws.com
```

### GitHub (source / binaries)
```
github.com/getlantern/lantern
github.com/getlantern/lantern-client
github.com/getlantern/lantern-server-manager
github.com/getlantern/systray
```

### Related
```
api.getlantern.org
update.getlantern.org
```

---

## 4. Hotspot Shield & Pango Group Family

Hotspot Shield is now owned by Aura (formerly Pango Group). The Pango portfolio includes **Hotspot Shield, Betternet, Touch VPN, VPN 360, Ultra VPN, Robo Shield**, all sharing infrastructure.

### Hotspot Shield
```
hotspotshield.com
www.hotspotshield.com
blog.hotspotshield.com
support.hotspotshield.com
hsselite.com
www.hsselite.com
api.hsselite.com
anchorfree.com
www.anchorfree.com
hss.anchorfree.com
afaccesssecurity.com
www.afaccesssecurity.com
s3.amazonaws.com/elitertrgtng
```

### Betternet (Pango Group)
```
betternet.co
www.betternet.co
betternet-backend.herokuapp.com
betternet.softonic.com
```

### Touch VPN (Pango Group)
```
touchvpn.net
www.touchvpn.net
support.touchvpn.net
```

### VPN 360 (Pango Group)
```
vpn360.com
www.vpn360.com
```

### Ultra VPN (Pango Group)
```
ultravpn.com
www.ultravpn.com
ultravpn.net
```

### Robo Shield (Pango Group)
```
roboshield.com
www.roboshield.com
```

---

## 5. Other Common Free VPNs

### Avira Phantom VPN
```
avira-vpn.com
www.avira-vpn.com
phantom.avira-vpn.com
api.phantom.avira-vpn.com
```

### ProtonVPN
```
protonvpn.com
www.protonvpn.com
api.protonvpn.com
account.protonvpn.com
```

### ExpressVPN
```
expressvpn.com
www.expressvpn.com
api.expressvpn.com
apidisco-aka.expressvpn.com
```

### VPNBook
```
vpnbook.com
www.vpnbook.com
```

### FreeVPN.me / FreeVPN.pw / FreeMicVPN
```
freevpn.me
freevpn.pw
freemicvpn.com
```

### Windscribe
```
windscribe.com
www.windscribe.com
```

### TunnelBear
```
tunnelbear.com
www.tunnelbear.com
```

### Hide.me
```
hide.me
www.hide.me
```

### VPN Gate / University of Tsukuba
```
vpngate.net
www.vpngate.net
```

---

## 6. Non‑VPN Domains (Collateral / False Positives — Flagged)

These domains are in the original list but are **NOT VPN services**. Blocking them will cause real collateral damage. They are listed here for review — recommend **removing** from any production blocklist unless there is a specific reason to keep them.

```
brave.com            # Brave browser — blocks browser updates
yandex.com           # Major Russian search engine / cloud
mitmproxy.org        # Open‑source MITM proxy tool (used by security teams)
noip.com             # Dynamic DNS provider (used by many legitimate services)
savefrom.net         # Online video downloader
salesforceliveagent.com  # Salesforce customer‑service chat
globalchatinc.com    # Live chat / customer service widget
proxygate.com        # Proxy service (not a VPN, but may be intentional)
piaproxy.net         # Private Internet Access proxy endpoint (paid VPN — intentional?)
proxy.googlezip.net  # Google's data‑compression proxy (breaks Google services)
```

---

## 7. ASN & IP Range Blocklist (Firewall Layer)

DNS blocks alone are insufficient — many VPNs use IP‑direct connections. Add these ASNs and IP ranges to your firewall ipset / ACL for Layer 1 blocking.

### Psiphon (own ASN)
```
AS214623 (2644819 Ontario)
185.222.106.0/24
199.244.103.0/24
205.237.92.0/24
```

### X‑VPN Hosting Providers (blocking these ASNs will catch most free‑tier X‑VPN servers)
```
# Major cloud providers used by free VPNs:
# These are LARGE providers — blocking their entire ASN will break many legitimate services.
# Instead, use IP‑level blocklists from FireHOL or similar.
#
# Known X‑VPN hosters (with IP counts):
#   Zenlayer        — 795 IPs
#   Scaleway        — 195 IPs
#   OVHcloud        — 85 IPs
#   OneProvider     — 39 IPs
#   Amazon AWS      — 36 IPs
#   BaCloud         — 35 IPs
#   CDN Infra       — 30 IPs
#   M247            — 22 IPs
#   GTHost          — 16 IPs
#   YUGS Networks   — 15 IPs
#   ServeTheWorld   — 14 IPs
#   Global Communication Network — 10 IPs
```

### Recommended ipset update script
```bash
#!/bin/sh
# /etc/cron.weekly/update-vpn-blocklist.sh
wget -q -O /tmp/firehol_proxies.netset \
  "https://raw.githubusercontent.com/firehol/blocklist-ipsets/master/proxies.netset"
wget -q -O /tmp/vpn-ipv4.txt \
  "https://raw.githubusercontent.com/X4BNet/lists_vpn/main/ipv4.txt"
cat /tmp/vpn-ipv4.txt /tmp/firehol_proxies.netset \
  | grep -v '^#' | sort -u > /etc/ipset/vpn-blocklist.txt
/sbin/ipset flush vpn-blocklist 2>/dev/null || /sbin/ipset create vpn-blocklist hash:net
while IFS= read -r ip; do
  /sbin/ipset add vpn-blocklist "$ip" 2>/dev/null
done < /etc/ipset/vpn-blocklist.txt
```

---

## 8. Quick‑Reference Summary (for Chromebook / Browser GPO)

```
xvpn.io, *.xvpn.io
getxvpn.com, getxweb.com, getxhelp.com, getvpnx.com
xvpn-web.com, x-web-vip.com, websitevpnx.com
psiphon.ca, psiphon3.com, psiphon3.net, psiphoneverywhere.com
psiphon.ca.com, psiphon.ca.net, cdn.psiphon.ca
getlantern.org, lantern.io, lantern.net
hotspotshield.com, hsselite.com, anchorfree.com
betternet.co, touchvpn.net, vpn360.com, ultravpn.com
avira-vpn.com, phantom.avira-vpn.com
```

---

> **Last updated:** June 2026  
> **Sources:** Netify.ai, psiphon.ca, getlantern.org, xvpn.io, hotspotshield.com, CT log analysis, community blocklist feeds  
> **Note:** Free VPNs change domains rapidly. Re‑check and update this list every 2–4 weeks. For IP‑level blocking, subscribe to the FireHOL proxies list and X4BNet VPN list (see script in §7).
