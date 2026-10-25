// confirmMatches — the typed confirmation of ConfirmDanger (the backend checks confirm_name the same way).
// Case-sensitive; only surrounding whitespace is forgiven; an empty expected value never matches.
export function confirmMatches(input, expected) {
  return typeof input === 'string' && typeof expected === 'string' && expected.trim() !== '' && input.trim() === expected.trim()
}
