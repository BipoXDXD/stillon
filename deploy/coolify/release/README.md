# Publicação automática pelo Coolify

Depois da CI bem-sucedida na main, o workflow Coolify release confere o commit exato e todos os gates. Imagens são transportadas em OCI por SSH, com digest e plataforma conferidos, para uma entrada restrita ao projeto. A VPS publica no recurso existente do Coolify e só registra sucesso após saúde real. Não há runner de PR nem token GitHub na VPS.

A publicação exige CI de **push na main para o SHA exato**, incluindo o job `publish`.
Um commit que suprima a CI não pode ser publicado por esse fluxo, mesmo que a CI do PR,
as análises de segurança ou o backup estejam aprovados. O workflow de CI atual não tem
acionamento manual. Para retomar a publicação, integrar por PR um novo commit sem marcador
de supressão, aguardar a CI completa e reconferir main, pendências, filas e armazenamento.
O acionamento manual de `Coolify release` também mantém essas exigências; não substitui a CI.

A chave COOLIFY_RELEASE_SSH_KEY e o known_hosts COOLIFY_RELEASE_KNOWN_HOSTS ficam no environment production, restrito à main. A variável COOLIFY_RELEASE_TARGET identifica a conta restrita. O token Coolify é local, root-only, com API limitada a IPs locais. Chaves de backup continuam separadas.

A configuração de containers permanece fixada no commit de infraestrutura revisado; releases só atualizam imagens. Volumes, bancos e assets não são removidos. preserved-assets.json bloqueia remoção ou alteração das mídias originais. Imagens antigas são retidas. Não há downgrade automático do schema; falha exige inspeção do deployment e do estado pending.json na VPS. Para rollback, verificar compatibilidade de schema e backup antes de publicar uma release anterior pelo procedimento administrativo.

Receptor: /opt/coolify-release/receiver.py; configuração /etc/coolify-release/<projeto>.json; recibos /var/lib/coolify-release/<projeto>/. Alterações do receptor no Git não atualizam automaticamente o executável root da VPS: exigem revisão e instalação administrativa.

O transporte OCI é limitado a 4 GiB e a expansão agregada das camadas a 16 GiB. O preflight
usa inventário administrativo de storage e orçamento por filesystem: quando staging e imagens
compartilham o mesmo filesystem, exige **36 GiB livres**. São margens de admissão, não quota
nem reserva física. A trava comum do host serializa os receptores participantes; pendências
duráveis de qualquer projeto impedem novas releases até reconciliação administrativa.
StillOn também mantém a trava original do backup, que recusa operação enquanto houver intent pendente.

O receptor compartilhado **2.0.2**, protocolo **2**, deve ter os mesmos bytes de Duora e GoblinShift.
Hash revisado: `1ccd1ada4777c80081c4f9bb2a64a500b1e221f794406319504d9e66cbb975cb`.
A confirmação está em [SYNC-2.0.2.md](SYNC-2.0.2.md); instalação coordenada, homologação,
trava global, orçamento de storage, reconciliação e rollback permanecem em [ISOLATION.md](ISOLATION.md).
Este registro não instala ou ativa o receptor na VPS.
