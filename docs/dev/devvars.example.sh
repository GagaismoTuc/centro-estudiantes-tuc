# Application database login (a member of ce_app, not the table owner).
export DATABASE_URL='postgres://CHANGE_ME_APP_USER:CHANGE_ME_PASSWORD@127.0.0.1:5432/centro?sslmode=disable'
# Owner connection used only for migrations.
export MIGRATE_DATABASE_URL='postgres://CHANGE_ME_OWNER:CHANGE_ME_PASSWORD@127.0.0.1:5432/centro?sslmode=disable'
# Base64 HMAC key of at least 32 random bytes; changing it invalidates unused access codes.
export ACCESS_CODE_KEY='CHANGE_ME_32_BYTES_BASE64'
# Base64 AES key of exactly 32 random bytes; retain it to preserve enrolled authenticators.
export TOTP_ENC_KEY='CHANGE_ME_32_BYTES_BASE64'
# Public web origin for emailed links.
export APP_BASE_URL='http://localhost:5173'
# Comma-separated trusted proxy CIDRs; empty means no trusted proxies.
export TRUSTED_PROXIES=''
# API listen address.
export ADDR='127.0.0.1:8080'
# SMTP host; leave empty to use MAIL_DEV_DIR instead.
export SMTP_HOST=''
# SMTP port (defaults to 587).
export SMTP_PORT='587'
# SMTP username, required when SMTP_HOST is set.
export SMTP_USERNAME='CHANGE_ME_SMTP_USERNAME'
# SMTP password, required when SMTP_HOST is set.
export SMTP_PASSWORD='CHANGE_ME_SMTP_PASSWORD'
# Sender address, required for SMTP; optional for file mail.
export MAIL_FROM='Centro de Estudiantes <no-reply@example.invalid>'
# Local directory for development emails when SMTP_HOST is empty.
export MAIL_DEV_DIR="$HOME/centro-dev/mail"
