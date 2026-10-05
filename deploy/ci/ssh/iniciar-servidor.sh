#!/bin/sh
# Instala somente fixtures no contêiner de teste, sem alterar o host.
set -eu
umask 077
install -d -m 0755 /run/sshd /var/lib/stillon-ssh
install -d -o deploy -g deploy -m 0700 /home/deploy/.ssh
{
    printf '%s ' 'restrict,command="sudo /usr/local/sbin/stillon-entrada"'
    cat /fixture/cliente.pub
} > /home/deploy/.ssh/authorized_keys
chown deploy:deploy /home/deploy/.ssh/authorized_keys
chmod 0600 /home/deploy/.ssh/authorized_keys
install -o root -g root -m 0440 /fixture/sudoers /etc/sudoers.d/stillon-entrada
install -o root -g root -m 0755 /usr/local/lib/stillon-ssh/entrada.sh /usr/local/sbin/stillon-entrada
visudo -cf /etc/sudoers.d/stillon-entrada
ssh -V
/usr/sbin/sshd -V
sudo --version | head -n 1
/usr/sbin/sshd -t -f /usr/local/lib/stillon-ssh/sshd_config
exec /usr/sbin/sshd -D -e -f /usr/local/lib/stillon-ssh/sshd_config
