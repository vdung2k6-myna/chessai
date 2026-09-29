// vi.js — the Vietnamese interface strings.
//
// The default locale. Vietnamese is the only locale here whose script is Latin,
// which is why the font stack in style.css puts the generic fallback before the
// CJK faces: a CJK face listed first would render the diacritics in ế ộ ữ ằ ỹ
// badly or drop them.
//
// Keys, placeholders and markup are the same as en.js; that table is the
// fallback, and testdata/ holds this one to its key set.
globalThis.XQ_LOCALES = Object.assign(globalThis.XQ_LOCALES || {}, {
  vi: {
    'app.name': 'Cờ Tướng',
    'app.tagline': 'Chơi với engine <strong>godogpaw</strong>, dùng <a href="https://www.chessdb.cn/cloudbook_api_en.html" target="_blank" rel="noreferrer">chessdb.cn</a> làm sách khai cuộc.',

    'board.label': 'Bàn cờ tướng',

    'status.loading': 'Đang tải…',
    'status.yourTurn': '{side} đi — đến lượt bạn',
    'status.yourTurnCheck': '{side} đi — đến lượt bạn (bạn đang bị chiếu)',
    'status.computerTurn': '{side} đi — đến lượt máy',
    'status.thinking': 'Máy đang suy nghĩ…',
    'status.draw': 'Hòa — {reason}',
    'status.winYou': '{winner} thắng bằng {reason} — bạn thắng',
    'status.winComputer': '{winner} thắng bằng {reason} — máy thắng',

    'side.red': 'Đỏ',
    'side.black': 'Đen',

    // How a game ended, keyed by the reason code the server sends.
    'reason.checkmate': 'chiếu bí',
    'reason.stalemate': 'hết nước',
    'reason.perpetual_check': 'chiếu dai',
    'reason.repetition': 'lặp lại ba lần',
    'reason.sixty_move': 'luật sáu mươi nước',
    'reason.unknown': 'một lý do không rõ',

    'aiMove.played': 'Máy đã đi <strong>{move}</strong>',
    'aiMove.detail': ' — {stats}',
    'badge.book': 'sách',
    'badge.search': 'tìm kiếm',
    'badge.fallback': 'dự phòng',
    'stat.depth': 'độ sâu {n}',
    'stat.score': 'điểm {n}',
    'stat.nodes': '{n} nút',
    'stat.time': '{n} ms',
    'list.separator': ', ',

    'pov.red': 'Điểm và tỉ lệ thắng tính theo góc nhìn của Đỏ.',
    'pov.black': 'Điểm và tỉ lệ thắng tính theo góc nhìn của Đen.',

    'analysis.heading': 'Phân tích',
    'analysis.cloudBest': 'Nước tốt nhất (cloud)',
    'analysis.cloudEval': 'Đánh giá (cloud)',
    'analysis.cloudDepth': 'Độ sâu (cloud)',
    'analysis.topMoves': 'Nước sách hàng đầu',
    'analysis.pv': 'Biến chính',
    'analysis.plies': '{n} nước',
    'analysis.notInBook': 'Thế cờ này không có trong cloudbook.',
    'analysis.partial': 'Một số truy vấn cloudbook thất bại; phần này có thể chưa đầy đủ.',
    'analysis.unavailable': 'Cloudbook không khả dụng: {error}',

    'table.move': 'Nước',
    'table.score': 'Điểm',
    'table.winrate': 'Tỉ lệ thắng %',
    'table.note': 'Ghi chú',

    'button.newGame': 'Ván mới',
    'button.undo': 'Đi lại',
    'button.hint': 'Gợi ý',

    'settings.language': 'Ngôn ngữ',
    'settings.playAs': 'Chơi quân',
    'settings.sideRed': 'Đỏ (đi trước)',
    'settings.sideBlack': 'Đen (đi sau)',
    'settings.level': 'Cấp độ',
    'settings.useBook': 'Dùng sách khai cuộc',
    'settings.bookNote': 'Trong khai cuộc, sách là nguồn các nước để máy chọn, ở mọi cấp độ, nên ngay cả cấp dễ nhất cũng mở đầu bằng những nước người chơi dễ nhận ra.',

    'level.beginner': 'Mới chơi',
    'level.easy': 'Dễ',
    'level.medium': 'Trung bình',
    'level.hard': 'Khó',
    'level.master': 'Cao thủ',

    'hint.fromBook': 'Gợi ý: đi {move} (từ sách khai cuộc)',
    'hint.fromSearch': 'Gợi ý: đi {move} (từ tìm kiếm độ sâu {depth})',

    // Failures, keyed by the codes the server sends. The server's own English
    // message is never shown, for the same reason it is not shown in English.
    'error.bad_move': 'Nước đó không hợp lệ ở thế cờ này.',
    'error.bad_square': 'Ô đó không có trên bàn cờ.',
    'error.bad_level': 'Cấp độ đó không có trong game này.',
    'error.bad_player_side': 'Bạn chỉ có thể chơi quân Đỏ hoặc Đen.',
    'error.bad_body': 'Yêu cầu không hợp lệ.',
    'error.not_your_turn': 'Chưa đến lượt bạn.',
    'error.not_ai_turn': 'Chưa đến lượt máy.',
    'error.game_over': 'Ván cờ đã kết thúc. Hãy bắt đầu ván mới để chơi tiếp.',
    'error.no_undo': 'Không có nước nào để đi lại.',
    'error.ai_busy': 'Máy đang suy nghĩ.',
    'error.stale': 'Ván cờ đã thay đổi trong lúc máy suy nghĩ. Hãy thử lại.',
    'error.offline': 'Sách khai cuộc không khả dụng.',
    'error.no_move': 'Không có nước hợp lệ nào để đi.',
    'error.internal': 'Máy chủ gặp lỗi.',
    'error.network': 'Máy chủ không phản hồi.',
    'error.unknown': 'Đã có lỗi xảy ra.',
  },
});
