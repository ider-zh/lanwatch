package asus

// normalScript 常规轮：对需复核的 IP 在路由器侧补 ping（结果以 "P <ip>" 行输出），
// 再读邻居表与 dnsmasq 租约。$1 为空格分隔的待复核 IP 列表（可为空）。
const normalScript = `#!/bin/sh
for ip in $1; do
  ( ping -c1 -W1 "$ip" >/dev/null 2>&1 && echo "P $ip" ) &
done
wait
echo "###NEIGH"
ip -4 neigh show dev %s
echo "###LEASES"
cat /tmp/dnsmasq.leases 2>/dev/null
`

// discoverScript 发现轮（每 N 轮）：全 /24 扫描（64 并发分 4 批），BusyBox 无 xargs -P。
// $1 为网段前缀（如 192.168.1）。
const discoverScript = `#!/bin/sh
BASE="$1"
i=1
while [ $i -le 254 ]; do
  j=0
  while [ $j -lt 64 ]; do
    n=$((i+j))
    if [ $n -le 254 ]; then
      ( ping -c1 -W1 "$BASE.$n" >/dev/null 2>&1 && echo "P $BASE.$n" ) &
    fi
    j=$((j+1))
  done
  wait
  i=$((i+64))
done
echo "###NEIGH"
ip -4 neigh show dev %s
echo "###LEASES"
cat /tmp/dnsmasq.leases 2>/dev/null
`
