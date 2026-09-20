/**
 * 약관과 개인정보 처리방침, 가입 화면의 동의 문구가 아직 초안인지.
 *
 * 법무 검토를 거친 문구로 바꾼 뒤에 false로 내린다. 참인 동안에는
 * - 문서와 가입 화면에 "초안"이라는 안내가 보이고,
 * - REQUIRE_REVIEWED_LEGAL=true로 빌드하면 빌드가 실패한다(vite.config.ts). 정식 배포본을 만드는 작업에서 이 변수를 켠다.
 *
 * 빌드 설정에서도 읽는 파일이다. 다른 모듈을 가져오지 않는다.
 */
export const LEGAL_DOCUMENTS_ARE_DRAFT = true;
