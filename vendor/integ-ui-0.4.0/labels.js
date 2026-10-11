import { logos } from './logos.js';
const keys = ['products','close','appearance','system','light','dark','account','manageAccount','navigation','home'];
const rows = {
 en:['Switch product','Close','Appearance','System','Light','Dark','Account','Manage account','Navigation','Integ.Life home'],
 zh:['切换产品','关闭','外观','跟随系统','浅色','深色','账户','管理账户','导航','Integ.Life 首页'],
 es:['Cambiar producto','Cerrar','Apariencia','Sistema','Claro','Oscuro','Cuenta','Gestionar cuenta','Navegación','Inicio de Integ.Life'],
 fr:['Changer de produit','Fermer','Apparence','Système','Clair','Sombre','Compte','Gérer le compte','Navigation','Accueil Integ.Life'],
 de:['Produkt wechseln','Schließen','Darstellung','System','Hell','Dunkel','Konto','Konto verwalten','Navigation','Integ.Life Startseite'],
 id:['Ganti produk','Tutup','Tampilan','Sistem','Terang','Gelap','Akun','Kelola akun','Navigasi','Beranda Integ.Life'],
 vi:['Chuyển sản phẩm','Đóng','Giao diện','Hệ thống','Sáng','Tối','Tài khoản','Quản lý tài khoản','Điều hướng','Trang chủ Integ.Life'],
 th:['สลับผลิตภัณฑ์','ปิด','รูปแบบ','ระบบ','สว่าง','มืด','บัญชี','จัดการบัญชี','การนำทาง','หน้าหลัก Integ.Life'],
 ja:['製品を切り替え','閉じる','外観','システム','ライト','ダーク','アカウント','アカウント管理','ナビゲーション','Integ.Life ホーム'],
 ko:['제품 전환','닫기','화면 모드','시스템','라이트','다크','계정','계정 관리','탐색','Integ.Life 홈'],
 hi:['उत्पाद बदलें','बंद करें','दिखावट','सिस्टम','हल्का','गहरा','खाता','खाता प्रबंधन','नेविगेशन','Integ.Life होम'],
 ar:['تبديل المنتج','إغلاق','المظهر','النظام','فاتح','داكن','الحساب','إدارة الحساب','التنقل','الرئيسية Integ.Life'],
};
export function shellLabels(locale = 'en') { const row=rows[locale.toLowerCase().split('-')[0]] || rows.en; return Object.fromEntries(keys.map((key,i)=>[key,row[i]])); }
export const products = [
 {id:'app',name:'Integ.Life',href:'https://app.integ.life/'},
 {id:'things',name:'Things',href:'https://things.integ.life/'},
 {id:'chat',name:'Integ.Chat',href:'https://chat.integ.life/'},
 {id:'lingulet',name:'Lingulet',href:'https://lang.integ.life/'},
 {id:'tools',name:'Integ Tools',href:'https://tools.integ.life/'},
 {id:'games',name:'Integ Games',href:'https://games.integ.life/'},
 {id:'chinese-chess',name:'Chinese Chess',href:'https://chinese-chess.integ.life/'},
 {id:'chess',name:'Chess',href:'https://chess.integ.life/'},
 {id:'snake',name:'Snake Arena',href:'https://snake.integ.life/'},
 {id:'blog',name:'Field Notes',href:'https://blog.integ.life/'},
 {id:'family',name:'Family Daily',href:'https://family.integ.life/'},
 {id:'context',name:'Context',href:'https://context.integ.life/'},
].map(item => ({...item, logo:logos[item.id]}));
const descriptions = {
 en:['Notes, tasks, people, money and calendar.','Catalog belongings and find where they are stored.','Collaborate in rooms, direct messages and threads.','Learn languages through reading, vocabulary and courses.','Browser tools for text, data and calculations.','Explore and play bite-sized browser games.','Learn Xiangqi, analyze positions and play matches.','Learn chess, analyze positions and play matches.','Play multiplayer Snake with friends.','Read articles about products, AI and focused work.','Share everyday moments and preserve family memories.',"Keep project records and bring verified context to AI."],
 zh:['笔记、待办、人物、记账与日历。','记录物品，查找它们的存放位置。','通过频道、私聊和话题与人及 AI 协作。','通过阅读、词汇和课程学习语言。','在浏览器中处理文字、数据与计算。','发现并畅玩轻量浏览器小游戏。','学习中国象棋、分析棋局与对弈。','学习国际象棋、分析棋局与对弈。','和朋友一起玩多人贪吃蛇。','阅读产品、AI 与专注工作相关的文章。','分享日常点滴，留存家庭记忆。',"留存项目记录，为 AI 提供可追溯的上下文。"],
 es:['Notas, tareas, contactos, finanzas y calendario.','Organiza tus objetos y encuentra dónde están.','Colabora en salas, mensajes directos e hilos.','Aprende idiomas con lecturas, vocabulario y cursos.','Herramientas de texto, datos y cálculo en el navegador.','Descubre pequeños juegos para el navegador.','Aprende xiangqi, analiza posiciones y juega.','Aprende ajedrez, analiza posiciones y juega.','Juega a Snake multijugador con amigos.','Artículos sobre productos, IA y trabajo enfocado.','Comparte el día a día y conserva recuerdos familiares.',"Guarda registros de proyectos y aporta contexto verificable a la IA."],
 fr:['Notes, tâches, contacts, finances et calendrier.','Répertoriez vos objets et retrouvez leur emplacement.','Collaborez dans des salons, messages et fils.','Apprenez les langues par la lecture et des cours.','Outils de texte, de données et de calcul en ligne.','Découvrez de petits jeux pour navigateur.','Apprenez le xiangqi, analysez et jouez.','Apprenez les échecs, analysez et jouez.','Jouez à Snake à plusieurs avec vos amis.','Articles sur les produits, l’IA et le travail concentré.','Partagez le quotidien et gardez les souvenirs de famille.',"Conservez les données de projet et un contexte vérifiable pour l’IA."],
 de:['Notizen, Aufgaben, Kontakte, Finanzen und Kalender.','Erfasse Gegenstände und finde ihren Aufbewahrungsort.','Arbeite in Räumen, Direktnachrichten und Threads zusammen.','Lerne Sprachen mit Lesetexten, Vokabeln und Kursen.','Browser-Werkzeuge für Text, Daten und Berechnungen.','Entdecke kleine Spiele für den Browser.','Lerne Xiangqi, analysiere Stellungen und spiele.','Lerne Schach, analysiere Stellungen und spiele.','Spiele Multiplayer-Snake mit Freunden.','Artikel über Produkte, KI und konzentriertes Arbeiten.','Teile den Alltag und bewahre Familienerinnerungen.',"Bewahre Projektaufzeichnungen und überprüfbaren Kontext für KI."],
 id:['Catatan, tugas, kontak, keuangan, dan kalender.','Catat barang dan temukan lokasi penyimpanannya.','Berkolaborasi lewat ruang, pesan langsung, dan utas.','Belajar bahasa melalui bacaan, kosakata, dan kursus.','Alat peramban untuk teks, data, dan perhitungan.','Temukan permainan singkat di peramban.','Belajar xiangqi, analisis posisi, dan bermain.','Belajar catur, analisis posisi, dan bermain.','Mainkan Snake bersama teman.','Artikel tentang produk, AI, dan kerja terfokus.','Bagikan keseharian dan simpan kenangan keluarga.',"Simpan catatan proyek dan konteks terverifikasi untuk AI."],
 vi:['Ghi chú, công việc, liên hệ, tài chính và lịch.','Ghi lại đồ đạc và tìm nơi cất giữ.','Cộng tác qua phòng, tin nhắn riêng và chủ đề.','Học ngôn ngữ qua bài đọc, từ vựng và khóa học.','Công cụ xử lý văn bản, dữ liệu và tính toán.','Khám phá trò chơi ngắn trên trình duyệt.','Học cờ tướng, phân tích thế cờ và thi đấu.','Học cờ vua, phân tích thế cờ và thi đấu.','Chơi Snake nhiều người cùng bạn bè.','Bài viết về sản phẩm, AI và làm việc tập trung.','Chia sẻ cuộc sống và lưu giữ kỷ niệm gia đình.',"Lưu bản ghi dự án và cung cấp ngữ cảnh có thể kiểm chứng cho AI."],
 th:['บันทึก งาน ผู้ติดต่อ การเงิน และปฏิทิน','บันทึกสิ่งของและค้นหาที่จัดเก็บ','ทำงานร่วมกันผ่านห้อง ข้อความส่วนตัว และเธรด','เรียนภาษาผ่านบทอ่าน คำศัพท์ และหลักสูตร','เครื่องมือข้อความ ข้อมูล และคำนวณในเบราว์เซอร์','ค้นพบเกมสั้น ๆ ที่เล่นได้ในเบราว์เซอร์','เรียนหมากรุกจีน วิเคราะห์กระดาน และเล่น','เรียนหมากรุกสากล วิเคราะห์กระดาน และเล่น','เล่นเกมงูหลายคนกับเพื่อน','อ่านเรื่องผลิตภัณฑ์ AI และการทำงานอย่างมีสมาธิ','แบ่งปันชีวิตประจำวันและเก็บความทรงจำครอบครัว',"เก็บบันทึกโครงการและส่งบริบทที่ตรวจสอบได้ให้ AI"],
 ja:['ノート、タスク、連絡先、家計、カレンダー。','持ち物を記録し、保管場所を見つける。','ルーム、個別メッセージ、スレッドで共同作業。','読書、語彙、コースで言語を学ぶ。','文章、データ、計算のブラウザーツール。','手軽なブラウザーゲームを見つけて遊ぶ。','シャンチーを学び、局面を分析して対局。','チェスを学び、局面を分析して対局。','友達とマルチプレイヤーのスネークで遊ぶ。','製品、AI、集中する働き方について読む。','日々の出来事を共有し、家族の思い出を残す。',"プロジェクトの記録を保存し、検証できる文脈をAIへ。"],
 ko:['노트, 할 일, 연락처, 가계부와 달력.','물건을 기록하고 보관 위치를 찾으세요.','방, 개인 메시지, 스레드로 협업하세요.','읽기, 어휘와 강좌로 언어를 배우세요.','텍스트, 데이터와 계산을 위한 브라우저 도구.','짧게 즐길 수 있는 브라우저 게임을 만나세요.','중국 장기를 배우고, 분석하고, 대국하세요.','체스를 배우고, 분석하고, 대국하세요.','친구들과 멀티플레이어 스네이크를 즐기세요.','제품, AI와 집중하는 일에 관한 글.','일상을 나누고 가족의 추억을 간직하세요.',"프로젝트 기록을 보관하고 검증 가능한 맥락을 AI에 제공하세요."],
 hi:['नोट्स, कार्य, संपर्क, वित्त और कैलेंडर।','सामान दर्ज करें और उसकी जगह खोजें।','रूम, निजी संदेश और थ्रेड में सहयोग करें।','पठन, शब्दावली और पाठ्यक्रम से भाषाएँ सीखें।','टेक्स्ट, डेटा और गणना के ब्राउज़र टूल।','छोटे ब्राउज़र गेम खोजें और खेलें।','चीनी शतरंज सीखें, विश्लेषण करें और खेलें।','शतरंज सीखें, विश्लेषण करें और खेलें।','दोस्तों के साथ मल्टीप्लेयर स्नेक खेलें।','उत्पादों, AI और एकाग्र कार्य पर लेख पढ़ें।','रोज़मर्रा के पल साझा करें और पारिवारिक यादें सहेजें।',"परियोजना रिकॉर्ड रखें और AI को सत्यापित संदर्भ दें।"],
 ar:['ملاحظات ومهام وجهات اتصال ومال وتقويم.','سجّل أغراضك واعثر على أماكن حفظها.','تعاون عبر الغرف والرسائل الخاصة والمناقشات.','تعلّم اللغات بالقراءة والمفردات والدورات.','أدوات للنصوص والبيانات والحسابات في المتصفح.','اكتشف ألعابًا قصيرة والعبها في المتصفح.','تعلّم الشطرنج الصيني وحلّل المواقف والعب.','تعلّم الشطرنج وحلّل المواقف والعب.','العب سنيك مع أصدقائك عبر الإنترنت.','مقالات عن المنتجات والذكاء الاصطناعي والعمل المركّز.','شارك تفاصيل الحياة واحتفظ بذكريات العائلة.',"احتفظ بسجلات المشاريع وقدّم سياقًا موثوقًا للذكاء الاصطناعي."],
};
export function productList(locale='en') {
 const copy=descriptions[locale.toLowerCase().split('-')[0]] || descriptions.en;
 return products.map((item,index)=>({...item,description:copy[index]}));
}

const categoryRows = {
 en:['Tools & life','Learning & reading','Games'],
 zh:['工具与生活','学习与阅读','游戏'],
 es:['Herramientas y vida','Aprender y leer','Juegos'],
 fr:['Outils et quotidien','Apprendre et lire','Jeux'],
 de:['Werkzeuge & Alltag','Lernen & Lesen','Spiele'],
 id:['Alat & keseharian','Belajar & membaca','Game'],
 vi:['Công cụ & cuộc sống','Học & đọc','Trò chơi'],
 th:['เครื่องมือและชีวิตประจำวัน','การเรียนรู้และการอ่าน','เกม'],
 ja:['ツールと暮らし','学習と読書','ゲーム'],
 ko:['도구와 일상','학습과 읽기','게임'],
 hi:['टूल और दैनिक जीवन','सीखना और पढ़ना','गेम'],
 ar:['الأدوات والحياة','التعلم والقراءة','الألعاب'],
};
const categories = [
 {id:'life',products:['app','things','chat','tools','family','context']},
 {id:'learn',products:['lingulet','blog']},
 {id:'play',products:['games','chinese-chess','chess','snake']},
];
export function productGroups(locale = 'en') {
 const labels=categoryRows[locale.toLowerCase().split('-')[0]] || categoryRows.en;
 const entries=productList(locale);
 return categories.map((group,index)=>({id:group.id,label:labels[index],products:group.products.map(id=>entries.find(item=>item.id===id))}));
}
