import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { MEDICAL_NOTICE_TEXT, MedicalNotice } from '@/components/MedicalNotice';

describe('MedicalNotice', () => {
  it('고정 고지 문구를 글자 그대로 보여 준다', () => {
    render(<MedicalNotice />);

    expect(screen.getByRole('note')).toHaveTextContent(
      /^내일은 의료 서비스가 아니며, 제공되는 정보는 진단이나 치료를 대신하지 않습니다\.$/,
    );
  });

  it('상수로 내보내는 문구도 같은 글자다', () => {
    expect(MEDICAL_NOTICE_TEXT).toBe(
      '내일은 의료 서비스가 아니며, 제공되는 정보는 진단이나 치료를 대신하지 않습니다.',
    );
  });
});
