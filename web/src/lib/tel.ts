/** 전화번호에서 숫자만 남겨 tel: 주소를 만든다. 걸 수 있는 숫자가 없으면 null이다. */
export function telHref(phone: string): string | null {
  const digits = phone.replace(/\D/g, '');
  return digits === '' ? null : `tel:${digits}`;
}
