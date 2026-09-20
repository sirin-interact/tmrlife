// 조회 키만 따로 둔다. 조회 클라이언트 설정과 인증 훅이 서로를 가져오지 않고 이 파일만 함께 본다.
export const meQueryKey = ['me'] as const;
export const authRequirementsQueryKey = ['auth', 'requirements'] as const;
