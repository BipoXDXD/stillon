# Publicação no Coolify

A publicação usa aplicações Git Compose em modo Raw, com repositório preservado e sem comando de start customizado. Os containers reais são pertencentes ao recurso no Coolify. Não há observador substituindo a aplicação.

Aplicação e infraestrutura têm recursos separados para que uma publicação da aplicação não pare o banco. As definições compartilham o nome Compose original, usam redes e volumes externos e não executam remoção de órfãos. Os binds privados existem previamente no host; suas credenciais não ficam no repositório. Cada imagem é fixada por digest e deve estar previamente importada e validada; `pull_policy: never` impede trocar a imagem durante o corte.

A configuração de preparação não executa escritores em segundo plano. Depois da cópia final consistente, a configuração definitiva deve habilitar os serviços completos antes da mudança de DNS.

Segredos de interpolação são configurados como variáveis privadas no Coolify. O modo Raw conserva o isolamento por serviço das variáveis e dos arquivos de segredo. Não adicionar env_file global a todos os serviços.

Manter backups e restauração verificada antes de atualizar uma versão. Não apontar tráfego para a origem com dados antigos após o destino aceitar escritas. Uma reversão precisa primeiro preservar e transferir o estado novo.
