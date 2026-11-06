// As tags de um monitor, no detalhe. Sem tags, nada: uma lista vazia só seria
// anunciada ao leitor de tela.
export function ListaDeTags({ tags }: { tags: readonly string[] }) {
  if (tags.length === 0) return null
  return (
    <ul aria-label="Tags" className="flex flex-wrap gap-1">
      {tags.map((tag) => (
        <li key={tag} className="rounded-full bg-painel px-2 text-meta text-suave">
          {tag}
        </li>
      ))}
    </ul>
  )
}
