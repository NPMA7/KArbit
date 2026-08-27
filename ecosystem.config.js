module.exports = {
  apps: [
    {
      name: 'karbit',
      script: './karbit',
      args: '-mode live -capital 10 -min-profit 0.05 -web-port 8080',
      cwd: '/var/www/KArbit',
      instances: 1,
      autorestart: true,
      watch: false,
      max_memory_restart: '500M',
      env: {
        NODE_ENV: 'production',
        KARBIT_MODE: 'live',
        PORT: '8080',
      },
      env_live: {
        NODE_ENV: 'production',
        KARBIT_MODE: 'live',
        PORT: '8080',
      },
      log_date_format: 'YYYY-MM-DD HH:mm:ss',
      error_file: '/var/www/KArbit/logs/err.log',
      out_file: '/var/www/KArbit/logs/out.log',
      merge_logs: true,
    },
  ],
};
