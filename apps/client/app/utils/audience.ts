// A rule as the audience editor holds it, and the way to and from the stored form.
//
// The stored grammar spells negation as a node of its own, `{ not: … }`, which is exact and
// awkward to edit: "not in Payers" would be a box around a row. The editor folds each `not`
// into the node it wraps as a flag instead, so a condition, a segment and a group can each be
// turned round in place, and a group reads as "All of", "Any of", "None of" or "Not all of".
// Every node carries an id, so a list re-renders a row rather than the row after it.

export type AudienceNode = AudienceGroupNode | AudienceConditionNode | AudienceSegmentNode

export interface AudienceGroupNode {
  id: string
  type: 'group'
  match: 'all' | 'any'
  negate: boolean
  children: AudienceNode[]
}

export interface AudienceConditionNode {
  id: string
  type: 'condition'
  negate: boolean
  key: string
  op: TargetOp
  value?: Scalar
  values?: Scalar[]
  /** For a key of the game's own, what its values are; inferred from them on the way in. */
  valueType?: AttributeType
}

export interface AudienceSegmentNode {
  id: string
  type: 'segment'
  negate: boolean
  segment: string
}

let counter = 0
export const audienceId = () => `node-${++counter}`

export function nodeFromRule(rule: TargetRule, negate = false): AudienceNode {
  if (rule.not) return nodeFromRule(rule.not, !negate)
  if (rule.all || rule.any) {
    return { id: audienceId(), type: 'group', match: rule.all ? 'all' : 'any', negate, children: (rule.all ?? rule.any ?? []).map(child => nodeFromRule(child)) }
  }
  if (rule.segment !== undefined) return { id: audienceId(), type: 'segment', negate, segment: rule.segment }
  if (rule.key === undefined && rule.op === undefined) return { id: audienceId(), type: 'group', match: 'all', negate, children: [] }
  const attribute = attributeOf(rule.key ?? '', rule.values ?? rule.value)
  return {
    id: audienceId(),
    type: 'condition',
    negate,
    key: rule.key ?? '',
    op: rule.op ?? 'eq',
    ...(rule.value === undefined ? {} : { value: rule.value }),
    ...(rule.values === undefined ? {} : { values: [...rule.values] }),
    valueType: attribute.type
  }
}

export function ruleFromNode(node: AudienceNode): TargetRule {
  let rule: TargetRule
  if (node.type === 'group') rule = { [node.match]: node.children.map(ruleFromNode) }
  else if (node.type === 'segment') rule = { segment: node.segment }
  else {
    const arity = OPERATORS[node.op].arity
    rule = {
      key: node.key,
      op: node.op,
      ...(arity === 'one' && node.value !== undefined ? { value: node.value } : {}),
      ...(arity === 'many' ? { values: node.values ?? [] } : {})
    }
  }
  return node.negate ? { not: rule } : rule
}

/** The root is always a group, so the editor has one place to add to. */
export function rootFromRule(rule: TargetRule): AudienceGroupNode {
  const node = nodeFromRule(rule)
  return node.type === 'group' ? node : { id: audienceId(), type: 'group', match: 'all', negate: false, children: [node] }
}

/** And back: no conditions is everyone, and a lone condition is stored as itself. */
export function ruleFromRoot(root: AudienceGroupNode): TargetRule {
  if (!root.children.length && !root.negate) return {}
  if (root.children.length === 1 && root.match === 'all' && !root.negate) return ruleFromNode(root.children[0]!)
  return ruleFromNode(root)
}

export function newCondition(): AudienceConditionNode {
  return { id: audienceId(), type: 'condition', negate: false, key: '', op: 'eq' }
}

export function newSegmentNode(segment = ''): AudienceSegmentNode {
  return { id: audienceId(), type: 'segment', negate: false, segment }
}

export function newGroup(): AudienceGroupNode {
  return { id: audienceId(), type: 'group', match: 'any', negate: false, children: [newCondition()] }
}

export type GroupMatch = 'all' | 'any' | 'none' | 'not_all'

export const GROUP_MATCHES: { value: GroupMatch, label: string, joiner: string }[] = [
  { value: 'all', label: 'All of', joiner: 'and' },
  { value: 'any', label: 'Any of', joiner: 'or' },
  { value: 'none', label: 'None of', joiner: 'or' },
  { value: 'not_all', label: 'Not all of', joiner: 'and' }
]

export const matchOf = (group: AudienceGroupNode): GroupMatch =>
  group.negate ? (group.match === 'any' ? 'none' : 'not_all') : group.match

export function withMatch(group: AudienceGroupNode, match: GroupMatch): AudienceGroupNode {
  return { ...group, match: match === 'any' || match === 'none' ? 'any' : 'all', negate: match === 'none' || match === 'not_all' }
}
