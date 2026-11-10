// A 64-symbol alphabet gives an unbiased 6 bits per random byte (144 bits total).
export function generatePassword(random = bytes => crypto.getRandomValues(bytes)) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';
  const bytes = random(new Uint8Array(24));
  return Array.from(bytes, byte => alphabet[byte & 63]).join('');
}
