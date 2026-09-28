// Historical delivery inventories remain readable and copyable. Folding is a
// view-only operation and never rewrites the persisted assistant response.
export function splitDeliveryListPresentation(content: string): {
  body: string;
  appendix: string;
  count: number;
} {
  const match = /\n\n(交付文件|Deliverables)\n\n([^]*?)\s*$/u.exec(content);
  if (!match) return { body: content, appendix: '', count: 0 };
  const lines = match[2].trim().split('\n');
  const artifactLink =
    /^- \[(?:\\.|[^\]\\])+\]\((?:\{\{artifact:[\w-]+\}\}|(?:https?:\/\/[^/\s]+)?\/api\/artifacts\/[\w-]+\/versions\/[\w-]+)\)\s*$/u;
  if (lines.length < 2 || !lines.every((line) => artifactLink.test(line))) {
    return { body: content, appendix: '', count: 0 };
  }
  return {
    body: content.slice(0, match.index),
    appendix: match[0].trim(),
    count: lines.length,
  };
}
