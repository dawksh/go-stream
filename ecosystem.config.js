module.exports = {
  apps: [
    {
      name: "go-torrent-stream",
      script: "bash",
      args: "-c 'source .env && exec ./go-stream -port 8080 -data /tmp/go-stream'",
      cwd: "/root/go-stream",
      exec_mode: "fork",
      instances: 1,
      autorestart: true,
      max_restarts: 10,
      restart_delay: 3000,
      watch: false,
      env: {
        PORT: 8080,
      },
    },
  ],
};
