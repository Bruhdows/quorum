// Join truthy class fragments. A one-line stand-in for clsx, which would be
// another dependency for something string concatenation already does.
export function cx(...parts) {
  return parts.filter(Boolean).join(' ');
}
