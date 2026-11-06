package clusterbootstrap

// Fixed native equivalent of the pinned installer's systemd server template.
// Do not execute install.sh: even SKIP_ENABLE/SKIP_START leaves its unconditional
// service disable/removal. Native publication never replaces or starts a unit.
// Ambient /etc/default, /etc/sysconfig and proxy environment are deliberately
// excluded. The only environment file is the exact empty, private file below.
const PrejoinK3sServiceUnit = `[Unit]
Description=Lightweight Kubernetes
Documentation=https://k3s.io
Wants=network-online.target
After=network-online.target

[Install]
WantedBy=multi-user.target

[Service]
Type=notify
EnvironmentFile=/etc/systemd/system/k3s.service.env
KillMode=process
Delegate=yes
User=root
LimitNOFILE=1048576
LimitNPROC=infinity
LimitCORE=infinity
TasksMax=infinity
TimeoutStartSec=0
Restart=always
RestartSec=5s
ExecStartPre=-/sbin/modprobe br_netfilter
ExecStartPre=-/sbin/modprobe overlay
ExecStart=/usr/local/bin/k3s server
`
