// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const apiFetchMock = vi.hoisted(() => vi.fn());
const showToastMock = vi.hoisted(() => vi.fn());
const redirectAfterAuthMock = vi.hoisted(() => vi.fn());

vi.mock("../dom/elements.js", () => ({
  app: document.body,
}));

vi.mock("../api.js", () => ({
  apiFetch: apiFetchMock,
}));

vi.mock("../utils.js", () => ({
  showToast: showToastMock,
  getAppVersion: () => "",
  redirectAfterAuth: redirectAfterAuthMock,
  escapeHTML: (s: string) =>
    String(s)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#039;"),
}));

const enCatalog = {
  "auth.2fa.accountFallback": "your account",
  "auth.2fa.failed": "Verification failed.",
  "auth.2fa.helper": "Enter the 6-digit code from your authenticator app, or a recovery code.",
  "auth.2fa.placeholder": "Code for {account}",
  "auth.2fa.submit": "Verify",
  "auth.2fa.title": "Two-factor authentication",
  "auth.actions.bootstrap": "Bootstrap",
  "auth.actions.login": "Login",
  "auth.actions.resetPassword": "Reset Password",
  "auth.bootstrap.failed": "Setup failed.",
  "auth.bootstrap.title": "First-time setup",
  "auth.fields.confirmPassword.label": "Confirm password",
  "auth.fields.confirmPassword.placeholder": "Confirm new password",
  "auth.fields.email.placeholder": "Email",
  "auth.fields.name.placeholder": "Name",
  "auth.fields.newPassword.label": "New password",
  "auth.fields.newPassword.placeholder": "Min 8 characters",
  "auth.fields.password.placeholder": "Password",
  "auth.forgot.backToSignIn": "Back to sign in",
  "auth.forgot.failed": "Could not request a password reset.",
  "auth.forgot.helper": "Enter your email. If an account exists for that email address, we’ll send a password reset link that expires in 30 minutes.",
  "auth.forgot.link": "Forgot password?",
  "auth.forgot.submit": "Send reset link",
  "auth.forgot.success": "If an account exists for that email address, a password reset email has been sent.",
  "auth.forgot.title": "Reset your password",
  "auth.login.failed": "Login failed.",
  "auth.oidc.button": "Continue with SSO",
  "auth.oidc.error.email": "A verified email address is required.",
  "auth.oidc.error.generic": "Authentication failed.",
  "auth.oidc.error.provider": "The identity provider returned an error.",
  "auth.oidc.error.state_invalid": "Login session expired or invalid. Please try again.",
  "auth.oidc.error.token": "Authentication failed. Please try again.",
  "auth.password.hide": "Hide password",
  "auth.password.show": "Show password",
  "auth.reset.helper": "Enter your new password. The link expires in 30 minutes.",
  "auth.reset.invalidLink": "Invalid or missing reset link",
  "auth.reset.invalidOrExpiredToken": "Invalid or expired reset token",
  "auth.reset.passwordsMismatch": "Passwords do not match",
  "auth.reset.success": "Password reset successfully. Please log in.",
  "auth.reset.title": "Reset Password",
  "auth.shared.helper": "Authentication is enabled for this instance. Anonymous boards remain shareable by URL; durable projects require sign-in.",
  "auth.shared.or": "or",
  "auth.signIn.title": "Sign in",
  "errors.RATE_LIMITED": "Too many attempts. Try again later.",
  "errors.UNAUTHORIZED": "Unauthorized",
  "errors.generic": "Something went wrong.",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "Language",
} as const;

const deCatalog = {
  "auth.2fa.accountFallback": "dein Konto",
  "auth.2fa.failed": "Verifizierung fehlgeschlagen.",
  "auth.2fa.helper": "Gib den 6-stelligen Code aus deiner Authenticator-App oder einen Wiederherstellungscode ein.",
  "auth.2fa.placeholder": "Code für {account}",
  "auth.2fa.submit": "Bestätigen",
  "auth.2fa.title": "Zwei-Faktor-Authentifizierung",
  "auth.actions.bootstrap": "Einrichten",
  "auth.actions.login": "Anmelden",
  "auth.actions.resetPassword": "Passwort zurücksetzen",
  "auth.bootstrap.failed": "Einrichtung fehlgeschlagen.",
  "auth.bootstrap.title": "Ersteinrichtung",
  "auth.fields.confirmPassword.label": "Passwort bestätigen",
  "auth.fields.confirmPassword.placeholder": "Neues Passwort bestätigen",
  "auth.fields.email.placeholder": "E-Mail",
  "auth.fields.name.placeholder": "Name",
  "auth.fields.newPassword.label": "Neues Passwort",
  "auth.fields.newPassword.placeholder": "Mindestens 8 Zeichen",
  "auth.fields.password.placeholder": "Passwort",
  "auth.forgot.backToSignIn": "Zurück zur Anmeldung",
  "auth.forgot.failed": "Die Anfrage zum Zurücksetzen des Passworts konnte nicht gesendet werden.",
  "auth.forgot.helper": "Gib deine E-Mail-Adresse ein. Falls ein Konto mit dieser Adresse existiert, senden wir einen Link zum Zurücksetzen, der in 30 Minuten abläuft.",
  "auth.forgot.link": "Passwort vergessen?",
  "auth.forgot.submit": "Link zum Zurücksetzen senden",
  "auth.forgot.success": "Falls ein Konto mit dieser E-Mail-Adresse existiert, wurde eine E-Mail zum Zurücksetzen des Passworts gesendet.",
  "auth.forgot.title": "Passwort zurücksetzen",
  "auth.login.failed": "Anmeldung fehlgeschlagen.",
  "auth.oidc.button": "Mit SSO fortfahren",
  "auth.oidc.error.email": "Eine verifizierte E-Mail-Adresse ist erforderlich.",
  "auth.oidc.error.generic": "Authentifizierung fehlgeschlagen.",
  "auth.oidc.error.provider": "Der Identitätsanbieter hat einen Fehler zurückgegeben.",
  "auth.oidc.error.state_invalid": "Die Anmeldesitzung ist abgelaufen oder ungültig. Bitte versuche es erneut.",
  "auth.oidc.error.token": "Authentifizierung fehlgeschlagen. Bitte versuche es erneut.",
  "auth.password.hide": "Passwort verbergen",
  "auth.password.show": "Passwort anzeigen",
  "auth.reset.helper": "Gib dein neues Passwort ein. Der Link läuft in 30 Minuten ab.",
  "auth.reset.invalidLink": "Ungültiger oder fehlender Zurücksetzungslink",
  "auth.reset.invalidOrExpiredToken": "Ungültiger oder abgelaufener Zurücksetzungslink",
  "auth.reset.passwordsMismatch": "Passwörter stimmen nicht überein",
  "auth.reset.success": "Passwort wurde zurückgesetzt. Bitte melde dich an.",
  "auth.reset.title": "Passwort zurücksetzen",
  "auth.shared.helper": "Für diese Instanz ist die Anmeldung aktiviert. Anonyme Boards bleiben per URL teilbar; dauerhafte Projekte erfordern eine Anmeldung.",
  "auth.shared.or": "oder",
  "auth.signIn.title": "Anmelden",
  "errors.RATE_LIMITED": "Zu viele Versuche. Bitte später erneut versuchen.",
  "errors.UNAUTHORIZED": "Nicht angemeldet",
  "errors.generic": "Etwas ist schiefgelaufen.",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "Sprache",
} as const;

const frCatalog = {
  "auth.2fa.accountFallback": "votre compte",
  "auth.2fa.failed": "Échec de la vérification.",
  "auth.2fa.helper": "Saisissez le code à 6 chiffres de votre application d’authentification ou un code de récupération.",
  "auth.2fa.placeholder": "Code pour {account}",
  "auth.2fa.submit": "Vérifier",
  "auth.2fa.title": "Authentification à deux facteurs",
  "auth.actions.bootstrap": "Initialiser",
  "auth.actions.login": "Se connecter",
  "auth.actions.resetPassword": "Réinitialiser le mot de passe",
  "auth.bootstrap.failed": "Échec de la configuration initiale.",
  "auth.bootstrap.title": "Configuration initiale",
  "auth.fields.confirmPassword.label": "Confirmer le mot de passe",
  "auth.fields.confirmPassword.placeholder": "Confirmer le nouveau mot de passe",
  "auth.fields.email.placeholder": "E-mail",
  "auth.fields.name.placeholder": "Nom",
  "auth.fields.newPassword.label": "Nouveau mot de passe",
  "auth.fields.newPassword.placeholder": "8 caractères minimum",
  "auth.fields.password.placeholder": "Mot de passe",
  "auth.login.failed": "Échec de la connexion.",
  "auth.oidc.button": "Continuer avec le SSO",
  "auth.oidc.error.email": "Une adresse e-mail vérifiée est requise.",
  "auth.oidc.error.generic": "L’authentification a échoué.",
  "auth.oidc.error.provider": "Le fournisseur d’identité a renvoyé une erreur.",
  "auth.oidc.error.state_invalid": "La session de connexion a expiré ou est invalide. Veuillez réessayer.",
  "auth.oidc.error.token": "L’authentification a échoué. Veuillez réessayer.",
  "auth.password.hide": "Masquer le mot de passe",
  "auth.password.show": "Afficher le mot de passe",
  "auth.reset.helper": "Saisissez votre nouveau mot de passe. Le lien expire dans 30 minutes.",
  "auth.reset.invalidLink": "Lien de réinitialisation invalide ou manquant",
  "auth.reset.invalidOrExpiredToken": "Lien de réinitialisation invalide ou expiré",
  "auth.reset.passwordsMismatch": "Les mots de passe ne correspondent pas",
  "auth.reset.success": "Mot de passe réinitialisé avec succès. Veuillez vous connecter.",
  "auth.reset.title": "Réinitialiser le mot de passe",
  "auth.shared.helper": "L’authentification est activée pour cette instance. Les tableaux anonymes restent partageables par URL ; les projets persistants nécessitent une connexion.",
  "auth.shared.or": "ou",
  "auth.signIn.title": "Se connecter",
  "errors.RATE_LIMITED": "Trop de tentatives. Réessayez plus tard.",
  "errors.UNAUTHORIZED": "Connexion requise",
  "errors.generic": "Une erreur s’est produite.",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "Langue",
} as const;

const ptCatalog = {
  "auth.2fa.accountFallback": "sua conta",
  "auth.2fa.failed": "Falha na verificação.",
  "auth.2fa.helper": "Digite o código de 6 dígitos do seu aplicativo autenticador ou um código de recuperação.",
  "auth.2fa.placeholder": "Código para {account}",
  "auth.2fa.submit": "Verificar",
  "auth.2fa.title": "Autenticação de dois fatores",
  "auth.actions.bootstrap": "Configurar",
  "auth.actions.login": "Entrar",
  "auth.actions.resetPassword": "Redefinir senha",
  "auth.bootstrap.failed": "Falha na configuração inicial.",
  "auth.bootstrap.title": "Configuração inicial",
  "auth.fields.confirmPassword.label": "Confirmar senha",
  "auth.fields.confirmPassword.placeholder": "Confirme a nova senha",
  "auth.fields.email.placeholder": "E-mail",
  "auth.fields.name.placeholder": "Nome",
  "auth.fields.newPassword.label": "Nova senha",
  "auth.fields.newPassword.placeholder": "Mínimo de 8 caracteres",
  "auth.fields.password.placeholder": "Senha",
  "auth.login.failed": "Falha no login.",
  "auth.oidc.button": "Continuar com SSO",
  "auth.oidc.error.email": "É necessário um endereço de e-mail verificado.",
  "auth.oidc.error.generic": "A autenticação falhou.",
  "auth.oidc.error.provider": "O provedor de identidade retornou um erro.",
  "auth.oidc.error.state_invalid": "A sessão de login expirou ou é inválida. Tente novamente.",
  "auth.oidc.error.token": "A autenticação falhou. Tente novamente.",
  "auth.password.hide": "Ocultar senha",
  "auth.password.show": "Mostrar senha",
  "auth.reset.helper": "Digite sua nova senha. O link expira em 30 minutos.",
  "auth.reset.invalidLink": "Link de redefinição inválido ou ausente",
  "auth.reset.invalidOrExpiredToken": "Link de redefinição inválido ou expirado",
  "auth.reset.passwordsMismatch": "As senhas não coincidem",
  "auth.reset.success": "Senha redefinida com sucesso. Faça login.",
  "auth.reset.title": "Redefinir senha",
  "auth.shared.helper": "A autenticação está ativada nesta instância. Quadros anônimos continuam compartilháveis por URL; projetos permanentes exigem login.",
  "auth.shared.or": "ou",
  "auth.signIn.title": "Entrar",
  "errors.RATE_LIMITED": "Muitas tentativas. Tente novamente mais tarde.",
  "errors.UNAUTHORIZED": "Login necessário",
  "errors.generic": "Algo deu errado.",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "Idioma",
} as const;

const arCatalog = {
  "auth.2fa.accountFallback": "حسابك",
  "auth.2fa.failed": "فشل التحقق.",
  "auth.2fa.helper": "أدخل الرمز المكوّن من 6 أرقام من تطبيق المصادقة، أو رمز استرداد.",
  "auth.2fa.placeholder": "رمز {account}",
  "auth.2fa.submit": "تحقق",
  "auth.2fa.title": "المصادقة الثنائية",
  "auth.actions.bootstrap": "إعداد أولي",
  "auth.actions.login": "تسجيل الدخول",
  "auth.actions.resetPassword": "إعادة تعيين كلمة المرور",
  "auth.bootstrap.failed": "فشل الإعداد.",
  "auth.bootstrap.title": "الإعداد لأول مرة",
  "auth.fields.confirmPassword.label": "تأكيد كلمة المرور",
  "auth.fields.confirmPassword.placeholder": "تأكيد كلمة المرور الجديدة",
  "auth.fields.email.placeholder": "البريد الإلكتروني",
  "auth.fields.name.placeholder": "الاسم",
  "auth.fields.newPassword.label": "كلمة مرور جديدة",
  "auth.fields.newPassword.placeholder": "8 أحرف على الأقل",
  "auth.fields.password.placeholder": "كلمة المرور",
  "auth.login.failed": "فشل تسجيل الدخول.",
  "auth.oidc.button": "المتابعة عبر SSO",
  "auth.oidc.error.email": "يلزم عنوان بريد إلكتروني موثّق.",
  "auth.oidc.error.generic": "فشلت المصادقة.",
  "auth.oidc.error.provider": "أعاد مزوّد الهوية خطأ.",
  "auth.oidc.error.state_invalid": "انتهت صلاحية جلسة تسجيل الدخول أو أنها غير صالحة. يرجى المحاولة مرة أخرى.",
  "auth.oidc.error.token": "فشلت المصادقة. يرجى المحاولة مرة أخرى.",
  "auth.password.hide": "إخفاء كلمة المرور",
  "auth.password.show": "إظهار كلمة المرور",
  "auth.reset.helper": "أدخل كلمة المرور الجديدة. ينتهي الرابط خلال 30 دقيقة.",
  "auth.reset.invalidLink": "رابط إعادة التعيين غير صالح أو مفقود",
  "auth.reset.invalidOrExpiredToken": "رمز إعادة التعيين غير صالح أو منتهٍ",
  "auth.reset.passwordsMismatch": "كلمتا المرور غير متطابقتين",
  "auth.reset.success": "تمت إعادة تعيين كلمة المرور بنجاح. يرجى تسجيل الدخول.",
  "auth.reset.title": "إعادة تعيين كلمة المرور",
  "auth.shared.helper": "المصادقة مفعّلة لهذه النسخة. تبقى اللوحات المجهولة قابلة للمشاركة عبر الرابط؛ المشاريع الدائمة تتطلب تسجيل الدخول.",
  "auth.shared.or": "أو",
  "auth.signIn.title": "تسجيل الدخول",
  "errors.RATE_LIMITED": "محاولات كثيرة جداً. حاول مرة أخرى لاحقاً.",
  "errors.UNAUTHORIZED": "غير مصرّح",
  "errors.generic": "حدث خطأ ما.",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "اللغة",
} as const;

const ruCatalog = {
  "auth.2fa.accountFallback": "ваш аккаунт",
  "auth.2fa.failed": "Проверка не удалась.",
  "auth.2fa.helper": "Введите 6-значный код из приложения-аутентификатора или код восстановления.",
  "auth.2fa.placeholder": "Код для {account}",
  "auth.2fa.submit": "Проверить",
  "auth.2fa.title": "Двухфакторная аутентификация",
  "auth.actions.bootstrap": "Настройка",
  "auth.actions.login": "Войти",
  "auth.actions.resetPassword": "Сбросить пароль",
  "auth.bootstrap.failed": "Настройка не удалась.",
  "auth.bootstrap.title": "Первоначальная настройка",
  "auth.fields.confirmPassword.label": "Подтвердите пароль",
  "auth.fields.confirmPassword.placeholder": "Подтвердите новый пароль",
  "auth.fields.email.placeholder": "Email",
  "auth.fields.name.placeholder": "Имя",
  "auth.fields.newPassword.label": "Новый пароль",
  "auth.fields.newPassword.placeholder": "Минимум 8 символов",
  "auth.fields.password.placeholder": "Пароль",
  "auth.login.failed": "Вход не удался.",
  "auth.oidc.button": "Продолжить через SSO",
  "auth.oidc.error.email": "Требуется подтверждённый адрес email.",
  "auth.oidc.error.generic": "Аутентификация не удалась.",
  "auth.oidc.error.provider": "Провайдер идентификации вернул ошибку.",
  "auth.oidc.error.state_invalid": "Сессия входа истекла или недействительна. Попробуйте снова.",
  "auth.oidc.error.token": "Аутентификация не удалась. Попробуйте снова.",
  "auth.password.hide": "Скрыть пароль",
  "auth.password.show": "Показать пароль",
  "auth.reset.helper": "Введите новый пароль. Ссылка действует 30 минут.",
  "auth.reset.invalidLink": "Недействительная или отсутствующая ссылка сброса",
  "auth.reset.invalidOrExpiredToken": "Недействительный или просроченный токен сброса",
  "auth.reset.passwordsMismatch": "Пароли не совпадают",
  "auth.reset.success": "Пароль успешно сброшен. Войдите в систему.",
  "auth.reset.title": "Сброс пароля",
  "auth.shared.helper": "Для этого экземпляра включена аутентификация. Анонимные доски остаются доступными по URL; постоянные проекты требуют входа.",
  "auth.shared.or": "или",
  "auth.signIn.title": "Вход",
  "errors.RATE_LIMITED": "Слишком много попыток. Попробуйте позже.",
  "errors.UNAUTHORIZED": "Не авторизован",
  "errors.generic": "Что-то пошло не так.",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "Язык",
} as const;

const jaCatalog = {
  "auth.2fa.accountFallback": "あなたのアカウント",
  "auth.2fa.failed": "認証に失敗しました。",
  "auth.2fa.helper": "認証アプリの6桁コード、またはリカバリーコードを入力してください。",
  "auth.2fa.placeholder": "{account} のコード",
  "auth.2fa.submit": "確認",
  "auth.2fa.title": "二要素認証",
  "auth.actions.bootstrap": "初期設定",
  "auth.actions.login": "ログイン",
  "auth.actions.resetPassword": "パスワードをリセット",
  "auth.bootstrap.failed": "セットアップに失敗しました。",
  "auth.bootstrap.title": "初回セットアップ",
  "auth.fields.confirmPassword.label": "パスワードの確認",
  "auth.fields.confirmPassword.placeholder": "新しいパスワードを再入力",
  "auth.fields.email.placeholder": "メールアドレス",
  "auth.fields.name.placeholder": "名前",
  "auth.fields.newPassword.label": "新しいパスワード",
  "auth.fields.newPassword.placeholder": "8文字以上",
  "auth.fields.password.placeholder": "パスワード",
  "auth.login.failed": "ログインに失敗しました。",
  "auth.oidc.button": "SSO で続行",
  "auth.oidc.error.email": "確認済みのメールアドレスが必要です。",
  "auth.oidc.error.generic": "認証に失敗しました。",
  "auth.oidc.error.provider": "ID プロバイダーがエラーを返しました。",
  "auth.oidc.error.state_invalid": "ログインセッションの有効期限が切れたか無効です。もう一度お試しください。",
  "auth.oidc.error.token": "認証に失敗しました。もう一度お試しください。",
  "auth.password.hide": "パスワードを非表示",
  "auth.password.show": "パスワードを表示",
  "auth.reset.helper": "新しいパスワードを入力してください。リンクは30分で失効します。",
  "auth.reset.invalidLink": "リセットリンクが無効または見つかりません",
  "auth.reset.invalidOrExpiredToken": "リセットトークンが無効または期限切れです",
  "auth.reset.passwordsMismatch": "パスワードが一致しません",
  "auth.reset.success": "パスワードをリセットしました。ログインしてください。",
  "auth.reset.title": "パスワードをリセット",
  "auth.shared.helper": "このインスタンスでは認証が有効です。匿名ボードは URL で共有可能です。永続的なプロジェクトにはサインインが必要です。",
  "auth.shared.or": "または",
  "auth.signIn.title": "サインイン",
  "errors.RATE_LIMITED": "試行回数が多すぎます。しばらくしてから再度お試しください。",
  "errors.UNAUTHORIZED": "認証が必要です",
  "errors.generic": "問題が発生しました。",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "言語",
} as const;

const trCatalog = {
  "auth.2fa.accountFallback": "hesabınız",
  "auth.2fa.failed": "Doğrulama başarısız.",
  "auth.2fa.helper": "Kimlik doğrulama uygulamanızdaki 6 haneli kodu veya bir kurtarma kodunu girin.",
  "auth.2fa.placeholder": "{account} için kod",
  "auth.2fa.submit": "Doğrula",
  "auth.2fa.title": "İki faktörlü kimlik doğrulama",
  "auth.actions.bootstrap": "Kurulum",
  "auth.actions.login": "Giriş yap",
  "auth.actions.resetPassword": "Parolayı sıfırla",
  "auth.bootstrap.failed": "Kurulum başarısız.",
  "auth.bootstrap.title": "İlk kurulum",
  "auth.fields.confirmPassword.label": "Parolayı onayla",
  "auth.fields.confirmPassword.placeholder": "Yeni parolayı onayla",
  "auth.fields.email.placeholder": "E-posta",
  "auth.fields.name.placeholder": "Ad",
  "auth.fields.newPassword.label": "Yeni parola",
  "auth.fields.newPassword.placeholder": "En az 8 karakter",
  "auth.fields.password.placeholder": "Parola",
  "auth.login.failed": "Giriş başarısız.",
  "auth.oidc.button": "SSO ile devam et",
  "auth.oidc.error.email": "Doğrulanmış bir e-posta adresi gerekli.",
  "auth.oidc.error.generic": "Kimlik doğrulama başarısız.",
  "auth.oidc.error.provider": "Kimlik sağlayıcı bir hata döndürdü.",
  "auth.oidc.error.state_invalid": "Oturum açma oturumu süresi doldu veya geçersiz. Lütfen tekrar deneyin.",
  "auth.oidc.error.token": "Kimlik doğrulama başarısız. Lütfen tekrar deneyin.",
  "auth.password.hide": "Parolayı gizle",
  "auth.password.show": "Parolayı göster",
  "auth.reset.helper": "Yeni parolanızı girin. Bağlantının süresi 30 dakika içinde dolacak.",
  "auth.reset.invalidLink": "Geçersiz veya eksik sıfırlama bağlantısı",
  "auth.reset.invalidOrExpiredToken": "Geçersiz veya süresi dolmuş sıfırlama jetonu",
  "auth.reset.passwordsMismatch": "Parolalar eşleşmiyor",
  "auth.reset.success": "Parola başarıyla sıfırlandı. Lütfen giriş yapın.",
  "auth.reset.title": "Parolayı sıfırla",
  "auth.shared.helper": "Bu örnek için kimlik doğrulama etkin. Anonim panolar URL ile paylaşılabilir; kalıcı projeler için oturum açmanız gerekir.",
  "auth.shared.or": "veya",
  "auth.signIn.title": "Oturum aç",
  "errors.RATE_LIMITED": "Çok fazla deneme. Daha sonra tekrar deneyin.",
  "errors.UNAUTHORIZED": "Yetkisiz",
  "errors.generic": "Bir şeyler ters gitti.",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "Dil",
} as const;

const koCatalog = {
  "auth.2fa.accountFallback": "내 계정",
  "auth.2fa.failed": "인증에 실패했습니다.",
  "auth.2fa.helper": "인증 앱의 6자리 코드 또는 복구 코드를 입력하세요.",
  "auth.2fa.placeholder": "{account}용 코드",
  "auth.2fa.submit": "인증",
  "auth.2fa.title": "2단계 인증",
  "auth.actions.bootstrap": "초기 설정",
  "auth.actions.login": "로그인",
  "auth.actions.resetPassword": "비밀번호 재설정",
  "auth.bootstrap.failed": "설정에 실패했습니다.",
  "auth.bootstrap.title": "최초 설정",
  "auth.fields.confirmPassword.label": "비밀번호 확인",
  "auth.fields.confirmPassword.placeholder": "새 비밀번호 확인",
  "auth.fields.email.placeholder": "이메일",
  "auth.fields.name.placeholder": "이름",
  "auth.fields.newPassword.label": "새 비밀번호",
  "auth.fields.newPassword.placeholder": "최소 8자",
  "auth.fields.password.placeholder": "비밀번호",
  "auth.login.failed": "로그인에 실패했습니다.",
  "auth.oidc.button": "SSO로 계속",
  "auth.oidc.error.email": "인증된 이메일 주소가 필요합니다.",
  "auth.oidc.error.generic": "인증에 실패했습니다.",
  "auth.oidc.error.provider": "ID 공급자가 오류를 반환했습니다.",
  "auth.oidc.error.state_invalid": "로그인 세션이 만료되었거나 유효하지 않습니다. 다시 시도하세요.",
  "auth.oidc.error.token": "인증에 실패했습니다. 다시 시도하세요.",
  "auth.password.hide": "비밀번호 숨기기",
  "auth.password.show": "비밀번호 표시",
  "auth.reset.helper": "새 비밀번호를 입력하세요. 링크는 30분 후 만료됩니다.",
  "auth.reset.invalidLink": "재설정 링크가 없거나 유효하지 않습니다",
  "auth.reset.invalidOrExpiredToken": "재설정 토큰이 유효하지 않거나 만료되었습니다",
  "auth.reset.passwordsMismatch": "비밀번호가 일치하지 않습니다",
  "auth.reset.success": "비밀번호가 재설정되었습니다. 로그인하세요.",
  "auth.reset.title": "비밀번호 재설정",
  "auth.shared.helper": "이 인스턴스에서는 인증이 활성화되어 있습니다. 익명 보드는 URL로 공유할 수 있으며, 영구 프로젝트에는 로그인이 필요합니다.",
  "auth.shared.or": "또는",
  "auth.signIn.title": "로그인",
  "errors.RATE_LIMITED": "시도 횟수가 너무 많습니다. 나중에 다시 시도하세요.",
  "errors.UNAUTHORIZED": "인증되지 않음",
  "errors.generic": "문제가 발생했습니다.",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "언어",
} as const;

const zhCatalog = {
  "auth.2fa.accountFallback": "你的账户",
  "auth.2fa.failed": "验证失败。",
  "auth.2fa.helper": "输入认证器应用中的 6 位验证码，或输入恢复码。",
  "auth.2fa.placeholder": "{account} 的验证码",
  "auth.2fa.submit": "验证",
  "auth.2fa.title": "双因素认证",
  "auth.actions.bootstrap": "初始化",
  "auth.actions.login": "登录",
  "auth.actions.resetPassword": "重置密码",
  "auth.bootstrap.failed": "设置失败。",
  "auth.bootstrap.title": "首次设置",
  "auth.fields.confirmPassword.label": "确认密码",
  "auth.fields.confirmPassword.placeholder": "确认新密码",
  "auth.fields.email.placeholder": "邮箱",
  "auth.fields.name.placeholder": "姓名",
  "auth.fields.newPassword.label": "新密码",
  "auth.fields.newPassword.placeholder": "至少 8 个字符",
  "auth.fields.password.placeholder": "密码",
  "auth.login.failed": "登录失败。",
  "auth.oidc.button": "使用 SSO 继续",
  "auth.oidc.error.email": "需要已验证的邮箱地址。",
  "auth.oidc.error.generic": "认证失败。",
  "auth.oidc.error.provider": "身份提供商返回了错误。",
  "auth.oidc.error.state_invalid": "登录会话已过期或无效。请重试。",
  "auth.oidc.error.token": "认证失败。请重试。",
  "auth.password.hide": "隐藏密码",
  "auth.password.show": "显示密码",
  "auth.reset.helper": "输入新密码。链接将在 30 分钟后过期。",
  "auth.reset.invalidLink": "重置链接无效或缺失",
  "auth.reset.invalidOrExpiredToken": "重置令牌无效或已过期",
  "auth.reset.passwordsMismatch": "两次输入的密码不一致",
  "auth.reset.success": "密码重置成功。请登录。",
  "auth.reset.title": "重置密码",
  "auth.shared.helper": "此实例已启用认证。匿名看板仍可通过 URL 分享；持久项目需要登录。",
  "auth.shared.or": "或",
  "auth.signIn.title": "登录",
  "errors.RATE_LIMITED": "尝试次数过多。请稍后再试。",
  "errors.UNAUTHORIZED": "未授权",
  "errors.generic": "出了点问题。",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "语言",
} as const;

const idCatalog = {
  "auth.2fa.accountFallback": "akun Anda",
  "auth.2fa.failed": "Verifikasi gagal.",
  "auth.2fa.helper": "Masukkan kode 6 digit dari aplikasi autentikator, atau kode pemulihan.",
  "auth.2fa.placeholder": "Kode untuk {account}",
  "auth.2fa.submit": "Verifikasi",
  "auth.2fa.title": "Autentikasi dua faktor",
  "auth.actions.bootstrap": "Penyiapan",
  "auth.actions.login": "Masuk",
  "auth.actions.resetPassword": "Atur ulang kata sandi",
  "auth.bootstrap.failed": "Penyiapan gagal.",
  "auth.bootstrap.title": "Penyiapan pertama",
  "auth.fields.confirmPassword.label": "Konfirmasi kata sandi",
  "auth.fields.confirmPassword.placeholder": "Konfirmasi kata sandi baru",
  "auth.fields.email.placeholder": "Email",
  "auth.fields.name.placeholder": "Nama",
  "auth.fields.newPassword.label": "Kata sandi baru",
  "auth.fields.newPassword.placeholder": "Min. 8 karakter",
  "auth.fields.password.placeholder": "Kata sandi",
  "auth.login.failed": "Gagal masuk.",
  "auth.oidc.button": "Lanjutkan dengan SSO",
  "auth.oidc.error.email": "Alamat email terverifikasi diperlukan.",
  "auth.oidc.error.generic": "Autentikasi gagal.",
  "auth.oidc.error.provider": "Penyedia identitas mengembalikan error.",
  "auth.oidc.error.state_invalid": "Sesi masuk kedaluwarsa atau tidak valid. Silakan coba lagi.",
  "auth.oidc.error.token": "Autentikasi gagal. Silakan coba lagi.",
  "auth.password.hide": "Sembunyikan kata sandi",
  "auth.password.show": "Tampilkan kata sandi",
  "auth.reset.helper": "Masukkan kata sandi baru. Tautan kedaluwarsa dalam 30 menit.",
  "auth.reset.invalidLink": "Tautan atur ulang tidak valid atau hilang",
  "auth.reset.invalidOrExpiredToken": "Token atur ulang tidak valid atau kedaluwarsa",
  "auth.reset.passwordsMismatch": "Kata sandi tidak cocok",
  "auth.reset.success": "Kata sandi berhasil diatur ulang. Silakan masuk.",
  "auth.reset.title": "Atur ulang kata sandi",
  "auth.shared.helper": "Autentikasi diaktifkan untuk instance ini. Papan anonim tetap dapat dibagikan lewat URL; proyek permanen memerlukan masuk.",
  "auth.shared.or": "atau",
  "auth.signIn.title": "Masuk",
  "errors.RATE_LIMITED": "Terlalu banyak percobaan. Coba lagi nanti.",
  "errors.UNAUTHORIZED": "Tidak diizinkan",
  "errors.generic": "Terjadi kesalahan.",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "Bahasa",
} as const;

const viCatalog = {
  "auth.2fa.accountFallback": "tài khoản của bạn",
  "auth.2fa.failed": "Xác minh thất bại.",
  "auth.2fa.helper": "Nhập mã 6 chữ số từ ứng dụng xác thực hoặc mã khôi phục.",
  "auth.2fa.placeholder": "Mã cho {account}",
  "auth.2fa.submit": "Xác minh",
  "auth.2fa.title": "Xác thực hai yếu tố",
  "auth.actions.bootstrap": "Thiết lập",
  "auth.actions.login": "Đăng nhập",
  "auth.actions.resetPassword": "Đặt lại mật khẩu",
  "auth.bootstrap.failed": "Thiết lập thất bại.",
  "auth.bootstrap.title": "Thiết lập lần đầu",
  "auth.fields.confirmPassword.label": "Xác nhận mật khẩu",
  "auth.fields.confirmPassword.placeholder": "Xác nhận mật khẩu mới",
  "auth.fields.email.placeholder": "Email",
  "auth.fields.name.placeholder": "Tên",
  "auth.fields.newPassword.label": "Mật khẩu mới",
  "auth.fields.newPassword.placeholder": "Tối thiểu 8 ký tự",
  "auth.fields.password.placeholder": "Mật khẩu",
  "auth.login.failed": "Đăng nhập thất bại.",
  "auth.oidc.button": "Tiếp tục với SSO",
  "auth.oidc.error.email": "Cần địa chỉ email đã xác minh.",
  "auth.oidc.error.generic": "Xác thực thất bại.",
  "auth.oidc.error.provider": "Nhà cung cấp danh tính trả về lỗi.",
  "auth.oidc.error.state_invalid": "Phiên đăng nhập đã hết hạn hoặc không hợp lệ. Vui lòng thử lại.",
  "auth.oidc.error.token": "Xác thực thất bại. Vui lòng thử lại.",
  "auth.password.hide": "Ẩn mật khẩu",
  "auth.password.show": "Hiện mật khẩu",
  "auth.reset.helper": "Nhập mật khẩu mới. Liên kết hết hạn sau 30 phút.",
  "auth.reset.invalidLink": "Liên kết đặt lại không hợp lệ hoặc thiếu",
  "auth.reset.invalidOrExpiredToken": "Mã đặt lại không hợp lệ hoặc đã hết hạn",
  "auth.reset.passwordsMismatch": "Mật khẩu không khớp",
  "auth.reset.success": "Đặt lại mật khẩu thành công. Vui lòng đăng nhập.",
  "auth.reset.title": "Đặt lại mật khẩu",
  "auth.shared.helper": "Xác thực được bật cho máy chủ này. Bảng ẩn danh vẫn có thể chia sẻ qua URL; dự án cần đăng nhập.",
  "auth.shared.or": "hoặc",
  "auth.signIn.title": "Đăng nhập",
  "errors.RATE_LIMITED": "Quá nhiều lần thử. Hãy thử lại sau.",
  "errors.UNAUTHORIZED": "Không được phép",
  "errors.generic": "Đã xảy ra lỗi.",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "Ngôn ngữ",
} as const;

const thCatalog = {
  "auth.2fa.accountFallback": "บัญชีของคุณ",
  "auth.2fa.failed": "การยืนยันล้มเหลว",
  "auth.2fa.helper": "ป้อนรหัส 6 หลักจากแอปยืนยันตัวตน หรือรหัสกู้คืน",
  "auth.2fa.placeholder": "รหัสสำหรับ {account}",
  "auth.2fa.submit": "ยืนยัน",
  "auth.2fa.title": "การยืนยันตัวตนสองขั้นตอน",
  "auth.actions.bootstrap": "เริ่มตั้งค่า",
  "auth.actions.login": "เข้าสู่ระบบ",
  "auth.actions.resetPassword": "รีเซ็ตรหัสผ่าน",
  "auth.bootstrap.failed": "การตั้งค่าล้มเหลว",
  "auth.bootstrap.title": "การตั้งค่าครั้งแรก",
  "auth.fields.confirmPassword.label": "ยืนยันรหัสผ่าน",
  "auth.fields.confirmPassword.placeholder": "ยืนยันรหัสผ่านใหม่",
  "auth.fields.email.placeholder": "อีเมล",
  "auth.fields.name.placeholder": "ชื่อ",
  "auth.fields.newPassword.label": "รหัสผ่านใหม่",
  "auth.fields.newPassword.placeholder": "อย่างน้อย 8 ตัวอักษร",
  "auth.fields.password.placeholder": "รหัสผ่าน",
  "auth.login.failed": "เข้าสู่ระบบล้มเหลว",
  "auth.oidc.button": "ดำเนินการต่อด้วย SSO",
  "auth.oidc.error.email": "ต้องมีที่อยู่อีเมลที่ยืนยันแล้ว",
  "auth.oidc.error.generic": "การยืนยันตัวตนล้มเหลว",
  "auth.oidc.error.provider": "ผู้ให้บริการตัวตนส่งคืนข้อผิดพลาด",
  "auth.oidc.error.state_invalid": "เซสชันเข้าสู่ระบบหมดอายุหรือไม่ถูกต้อง โปรดลองอีกครั้ง",
  "auth.oidc.error.token": "การยืนยันตัวตนล้มเหลว โปรดลองอีกครั้ง",
  "auth.password.hide": "ซ่อนรหัสผ่าน",
  "auth.password.show": "แสดงรหัสผ่าน",
  "auth.reset.helper": "ป้อนรหัสผ่านใหม่ ลิงก์จะหมดอายุใน 30 นาที",
  "auth.reset.invalidLink": "ลิงก์รีเซ็ตไม่ถูกต้องหรือไม่มี",
  "auth.reset.invalidOrExpiredToken": "โทเค็นรีเซ็ตไม่ถูกต้องหรือหมดอายุ",
  "auth.reset.passwordsMismatch": "รหัสผ่านไม่ตรงกัน",
  "auth.reset.success": "รีเซ็ตรหัสผ่านสำเร็จ โปรดเข้าสู่ระบบ",
  "auth.reset.title": "รีเซ็ตรหัสผ่าน",
  "auth.shared.helper": "เปิดใช้การยืนยันตัวตนสำหรับการติดตั้งนี้ บอร์ดไม่ระบุตัวตนยังแชร์ผ่าน URL ได้ โปรเจกต์ถาวรต้องเข้าสู่ระบบ",
  "auth.shared.or": "หรือ",
  "auth.signIn.title": "เข้าสู่ระบบ",
  "errors.RATE_LIMITED": "ลองมากเกินไป โปรดลองอีกครั้งภายหลัง",
  "errors.UNAUTHORIZED": "ต้องเข้าสู่ระบบ",
  "errors.generic": "เกิดข้อผิดพลาด",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "ภาษา",
} as const;

const urCatalog = {
  "auth.2fa.accountFallback": "آپ کا اکاؤنٹ",
  "auth.2fa.failed": "تصدیق ناکام رہی۔",
  "auth.2fa.helper": "تصدیقی ایپ سے 6 ہندسوں کا کوڈ یا بحالی کوڈ درج کریں۔",
  "auth.2fa.placeholder": "{account} کے لیے کوڈ",
  "auth.2fa.submit": "تصدیق کریں",
  "auth.2fa.title": "دو عنصر کی تصدیق",
  "auth.actions.bootstrap": "ابتدائی سیٹ اپ",
  "auth.actions.login": "لاگ ان",
  "auth.actions.resetPassword": "پاس ورڈ ری سیٹ",
  "auth.bootstrap.failed": "سیٹ اپ ناکام رہا۔",
  "auth.bootstrap.title": "پہلی بار سیٹ اپ",
  "auth.fields.confirmPassword.label": "پاس ورڈ کی تصدیق",
  "auth.fields.confirmPassword.placeholder": "نیا پاس ورڈ دوبارہ درج کریں",
  "auth.fields.email.placeholder": "ای میل",
  "auth.fields.name.placeholder": "نام",
  "auth.fields.newPassword.label": "نیا پاس ورڈ",
  "auth.fields.newPassword.placeholder": "کم از کم 8 حروف",
  "auth.fields.password.placeholder": "پاس ورڈ",
  "auth.login.failed": "لاگ ان ناکام رہا۔",
  "auth.oidc.button": "SSO کے ساتھ جاری رکھیں",
  "auth.oidc.error.email": "تصدیق شدہ ای میل پتہ ضروری ہے۔",
  "auth.oidc.error.generic": "تصدیق ناکام رہی۔",
  "auth.oidc.error.provider": "شناختی فراہم کنندہ نے خرابی واپس کی۔",
  "auth.oidc.error.state_invalid": "لاگ ان سیشن ختم یا غلط ہے۔ براہ کرم دوبارہ کوشش کریں۔",
  "auth.oidc.error.token": "تصدیق ناکام رہی۔ براہ کرم دوبارہ کوشش کریں۔",
  "auth.password.hide": "پاس ورڈ چھپائیں",
  "auth.password.show": "پاس ورڈ دکھائیں",
  "auth.reset.helper": "اپنا نیا پاس ورڈ درج کریں۔ لنک 30 منٹ میں ختم ہو جائے گا۔",
  "auth.reset.invalidLink": "ری سیٹ لنک غلط یا موجود نہیں",
  "auth.reset.invalidOrExpiredToken": "ری سیٹ ٹوکن غلط یا ختم ہو چکا",
  "auth.reset.passwordsMismatch": "پاس ورڈ مماثل نہیں",
  "auth.reset.success": "پاس ورڈ کامیابی سے ری سیٹ ہو گیا۔ براہ کرم لاگ ان کریں۔",
  "auth.reset.title": "پاس ورڈ ری سیٹ",
  "auth.shared.helper": "اس انسٹالیشن پر تصدیق فعال ہے۔ گمنام بورڈز URL سے شیئر ہو سکتے ہیں؛ مستقل پروجیکٹس کے لیے سائن ان ضروری ہے۔",
  "auth.shared.or": "یا",
  "auth.signIn.title": "سائن ان",
  "errors.RATE_LIMITED": "بہت زیادہ کوششیں۔ بعد میں کوشش کریں۔",
  "errors.UNAUTHORIZED": "غیر مجاز",
  "errors.generic": "کچھ غلط ہو گیا۔",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "زبان",
} as const;

const hiCatalog = {
  "auth.2fa.accountFallback": "आपका खाता",
  "auth.2fa.failed": "सत्यापन विफल रहा।",
  "auth.2fa.helper": "प्रमाणक ऐप से 6 अंकों का कोड या रिकवरी कोड दर्ज करें।",
  "auth.2fa.placeholder": "{account} के लिए कोड",
  "auth.2fa.submit": "सत्यापित करें",
  "auth.2fa.title": "दो-कारक प्रमाणीकरण",
  "auth.actions.bootstrap": "प्रारंभिक सेटअप",
  "auth.actions.login": "लॉग इन",
  "auth.actions.resetPassword": "पासवर्ड रीसेट",
  "auth.bootstrap.failed": "सेटअप विफल रहा।",
  "auth.bootstrap.title": "पहली बार सेटअप",
  "auth.fields.confirmPassword.label": "पासवर्ड की पुष्टि",
  "auth.fields.confirmPassword.placeholder": "नया पासवर्ड दोबारा दर्ज करें",
  "auth.fields.email.placeholder": "ईमेल",
  "auth.fields.name.placeholder": "नाम",
  "auth.fields.newPassword.label": "नया पासवर्ड",
  "auth.fields.newPassword.placeholder": "न्यूनतम 8 अक्षर",
  "auth.fields.password.placeholder": "पासवर्ड",
  "auth.login.failed": "लॉग इन विफल रहा।",
  "auth.oidc.button": "SSO के साथ जारी रखें",
  "auth.oidc.error.email": "सत्यापित ईमेल पता आवश्यक है।",
  "auth.oidc.error.generic": "प्रमाणीकरण विफल रहा।",
  "auth.oidc.error.provider": "पहचान प्रदाता ने त्रुटि लौटाई।",
  "auth.oidc.error.state_invalid": "लॉग इन सत्र समाप्त या अमान्य है। कृपया पुनः प्रयास करें।",
  "auth.oidc.error.token": "प्रमाणीकरण विफल रहा। कृपया पुनः प्रयास करें।",
  "auth.password.hide": "पासवर्ड छिपाएँ",
  "auth.password.show": "पासवर्ड दिखाएँ",
  "auth.reset.helper": "अपना नया पासवर्ड दर्ज करें। लिंक 30 मिनट में समाप्त हो जाएगा।",
  "auth.reset.invalidLink": "रीसेट लिंक अमान्य या अनुपस्थित",
  "auth.reset.invalidOrExpiredToken": "रीसेट टोकन अमान्य या समाप्त",
  "auth.reset.passwordsMismatch": "पासवर्ड मेल नहीं खाते",
  "auth.reset.success": "पासवर्ड सफलतापूर्वक रीसेट हो गया। कृपया लॉग इन करें।",
  "auth.reset.title": "पासवर्ड रीसेट",
  "auth.shared.helper": "इस इंस्टॉलेशन पर प्रमाणीकरण सक्षम है। अनाम बोर्ड URL से साझा किए जा सकते हैं; स्थायी परियोजनाओं के लिए साइन इन आवश्यक है।",
  "auth.shared.or": "या",
  "auth.signIn.title": "साइन इन",
  "errors.RATE_LIMITED": "बहुत अधिक प्रयास। बाद में पुनः प्रयास करें।",
  "errors.UNAUTHORIZED": "अनधिकृत",
  "errors.generic": "कुछ गलत हो गया।",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "भाषा",
} as const;

const esCatalog = {
  "auth.2fa.accountFallback": "tu cuenta",
  "auth.2fa.failed": "La verificación falló.",
  "auth.2fa.helper": "Ingresa el código de 6 dígitos de tu app de autenticación o un código de recuperación.",
  "auth.2fa.placeholder": "Código para {account}",
  "auth.2fa.submit": "Verificar",
  "auth.2fa.title": "Autenticación de dos factores",
  "auth.actions.bootstrap": "Configuración inicial",
  "auth.actions.login": "Iniciar sesión",
  "auth.actions.resetPassword": "Restablecer contraseña",
  "auth.bootstrap.failed": "La configuración falló.",
  "auth.bootstrap.title": "Configuración inicial",
  "auth.fields.confirmPassword.label": "Confirmar contraseña",
  "auth.fields.confirmPassword.placeholder": "Confirmar nueva contraseña",
  "auth.fields.email.placeholder": "Correo electrónico",
  "auth.fields.name.placeholder": "Nombre",
  "auth.fields.newPassword.label": "Nueva contraseña",
  "auth.fields.newPassword.placeholder": "Mínimo 8 caracteres",
  "auth.fields.password.placeholder": "Contraseña",
  "auth.login.failed": "Error al iniciar sesión.",
  "auth.oidc.button": "Continuar con SSO",
  "auth.oidc.error.email": "Se requiere una dirección de correo verificada.",
  "auth.oidc.error.generic": "La autenticación falló.",
  "auth.oidc.error.provider": "El proveedor de identidad devolvió un error.",
  "auth.oidc.error.state_invalid": "La sesión de inicio expiró o no es válida. Inténtalo de nuevo.",
  "auth.oidc.error.token": "La autenticación falló. Inténtalo de nuevo.",
  "auth.password.hide": "Ocultar contraseña",
  "auth.password.show": "Mostrar contraseña",
  "auth.reset.helper": "Ingresa tu nueva contraseña. El enlace expira en 30 minutos.",
  "auth.reset.invalidLink": "Enlace de restablecimiento inválido o faltante",
  "auth.reset.invalidOrExpiredToken": "Token de restablecimiento inválido o expirado",
  "auth.reset.passwordsMismatch": "Las contraseñas no coinciden",
  "auth.reset.success": "Contraseña restablecida correctamente. Inicia sesión.",
  "auth.reset.title": "Restablecer contraseña",
  "auth.shared.helper": "La autenticación está habilitada en esta instalación. Los tableros anónimos siguen compartiéndose por URL; los proyectos permanentes requieren iniciar sesión.",
  "auth.shared.or": "o",
  "auth.signIn.title": "Iniciar sesión",
  "errors.RATE_LIMITED": "Demasiados intentos. Inténtalo más tarde.",
  "errors.UNAUTHORIZED": "No autorizado",
  "errors.generic": "Algo salió mal.",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "Idioma",
} as const;

const itCatalog = {
  "auth.2fa.accountFallback": "il tuo account",
  "auth.2fa.failed": "Verifica non riuscita.",
  "auth.2fa.helper": "Inserisci il codice a 6 cifre dalla tua app di autenticazione o un codice di recupero.",
  "auth.2fa.placeholder": "Codice per {account}",
  "auth.2fa.submit": "Verifica",
  "auth.2fa.title": "Autenticazione a due fattori",
  "auth.actions.bootstrap": "Configurazione iniziale",
  "auth.actions.login": "Accedi",
  "auth.actions.resetPassword": "Reimposta password",
  "auth.bootstrap.failed": "Configurazione non riuscita.",
  "auth.bootstrap.title": "Configurazione iniziale",
  "auth.fields.confirmPassword.label": "Conferma password",
  "auth.fields.confirmPassword.placeholder": "Conferma la nuova password",
  "auth.fields.email.placeholder": "Email",
  "auth.fields.name.placeholder": "Nome",
  "auth.fields.newPassword.label": "Nuova password",
  "auth.fields.newPassword.placeholder": "Min. 8 caratteri",
  "auth.fields.password.placeholder": "Password",
  "auth.login.failed": "Accesso non riuscito.",
  "auth.oidc.button": "Continua con SSO",
  "auth.oidc.error.email": "È richiesto un indirizzo email verificato.",
  "auth.oidc.error.generic": "Autenticazione non riuscita.",
  "auth.oidc.error.provider": "Il provider di identità ha restituito un errore.",
  "auth.oidc.error.state_invalid": "Sessione di accesso scaduta o non valida. Riprova.",
  "auth.oidc.error.token": "Autenticazione non riuscita. Riprova.",
  "auth.password.hide": "Nascondi password",
  "auth.password.show": "Mostra password",
  "auth.reset.helper": "Inserisci la nuova password. Il link scade tra 30 minuti.",
  "auth.reset.invalidLink": "Link di reimpostazione non valido o mancante",
  "auth.reset.invalidOrExpiredToken": "Token di reimpostazione non valido o scaduto",
  "auth.reset.passwordsMismatch": "Le password non corrispondono",
  "auth.reset.success": "Password reimpostata. Accedi.",
  "auth.reset.title": "Reimposta password",
  "auth.shared.helper": "L'autenticazione è abilitata per questa installazione. Le board anonime restano condivisibili tramite URL; i progetti persistenti richiedono l'accesso.",
  "auth.shared.or": "oppure",
  "auth.signIn.title": "Accedi",
  "errors.RATE_LIMITED": "Troppi tentativi. Riprova più tardi.",
  "errors.UNAUTHORIZED": "Non autorizzato",
  "errors.generic": "Qualcosa è andato storto.",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "Lingua",
} as const;

const pseudoCatalog = {
  "auth.2fa.accountFallback": "[!! your account !!]",
  "auth.2fa.failed": "[!! Verification failed. !!]",
  "auth.2fa.helper": "[!! Enter the 6-digit code from your authenticator app, or a recovery code. !!]",
  "auth.2fa.placeholder": "[!! Code for {account} !!]",
  "auth.2fa.submit": "[!! Verify !!]",
  "auth.2fa.title": "[!! Two-factor authentication !!]",
  "auth.actions.bootstrap": "[!! Bootstrap !!]",
  "auth.actions.login": "[!! Login !!]",
  "auth.actions.resetPassword": "[!! Reset Password !!]",
  "auth.bootstrap.failed": "[!! Setup failed. !!]",
  "auth.bootstrap.title": "[!! First-time setup !!]",
  "auth.fields.confirmPassword.label": "[!! Confirm password !!]",
  "auth.fields.confirmPassword.placeholder": "[!! Confirm new password !!]",
  "auth.fields.email.placeholder": "[!! Email !!]",
  "auth.fields.name.placeholder": "[!! Name !!]",
  "auth.fields.newPassword.label": "[!! New password !!]",
  "auth.fields.newPassword.placeholder": "[!! Min 8 characters !!]",
  "auth.fields.password.placeholder": "[!! Password !!]",
  "auth.forgot.backToSignIn": "[!! Back to sign in !!]",
  "auth.forgot.failed": "[!! Could not request a password reset. !!]",
  "auth.forgot.helper": "[!! Enter your email. If an account exists for that email address, we’ll send a password reset link that expires in 30 minutes. !!]",
  "auth.forgot.link": "[!! Forgot password? !!]",
  "auth.forgot.submit": "[!! Send reset link !!]",
  "auth.forgot.success": "[!! If an account exists for that email address, a password reset email has been sent. !!]",
  "auth.forgot.title": "[!! Reset your password !!]",
  "auth.login.failed": "[!! Login failed. !!]",
  "auth.oidc.button": "[!! Continue with SSO !!]",
  "auth.oidc.error.email": "[!! A verified email address is required. !!]",
  "auth.oidc.error.generic": "[!! Authentication failed. !!]",
  "auth.oidc.error.provider": "[!! The identity provider returned an error. !!]",
  "auth.oidc.error.state_invalid": "[!! Login session expired or invalid. Please try again. !!]",
  "auth.oidc.error.token": "[!! Authentication failed. Please try again. !!]",
  "auth.password.hide": "[!! Hide password !!]",
  "auth.password.show": "[!! Show password !!]",
  "auth.reset.helper": "[!! Enter your new password. The link expires in 30 minutes. !!]",
  "auth.reset.invalidLink": "[!! Invalid or missing reset link !!]",
  "auth.reset.invalidOrExpiredToken": "[!! Invalid or expired reset token !!]",
  "auth.reset.passwordsMismatch": "[!! Passwords do not match !!]",
  "auth.reset.success": "[!! Password reset successfully. Please log in. !!]",
  "auth.reset.title": "[!! Reset Password !!]",
  "auth.shared.helper": "[!! Authentication is enabled for this instance. Anonymous boards remain shareable by URL; durable projects require sign-in. !!]",
  "auth.shared.or": "[!! or !!]",
  "auth.signIn.title": "[!! Sign in !!]",
  "errors.RATE_LIMITED": "[!! Too many attempts. Try again later. !!]",
  "errors.UNAUTHORIZED": "[!! Unauthorized !!]",
  "errors.generic": "[!! Something went wrong. !!]",
  "errors.httpStatus": "[!! HTTP {status} !!]",
  "settings.language.selectLabel": "[!! Language !!]",
} as const;

type TestLocale = "en" | "de" | "fr" | "pt" | "es" | "it" | "ar" | "ru" | "ja" | "tr" | "ko" | "zh" | "id" | "vi" | "th" | "ur" | "hi" | "pseudo";

function loader() {
  return vi.fn(async (locale: TestLocale) => {
    const catalogs = {
      en: enCatalog,
      de: deCatalog,
      fr: frCatalog,
      pt: ptCatalog,
      es: esCatalog,
      it: itCatalog,
      ar: arCatalog,
      ru: ruCatalog,
      ja: jaCatalog,
      tr: trCatalog,
      ko: koCatalog,
      zh: zhCatalog,
      id: idCatalog,
      vi: viCatalog,
      th: thCatalog,
      ur: urCatalog,
      hi: hiCatalog,
      pseudo: pseudoCatalog,
    };
    return catalogs[locale];
  });
}

async function setupI18n(locale: TestLocale = "en") {
  const i18n = await import("../i18n/index.js");
  await i18n.initI18n({
    locale,
    loadLocale: loader(),
  });
  return i18n;
}

async function flushPromises(count = 8): Promise<void> {
  for (let i = 0; i < count; i += 1) {
    await Promise.resolve();
  }
}

function getAuthLocaleSelect(): HTMLButtonElement {
  const button = document.getElementById("authLocaleSelect") as HTMLButtonElement | null;
  if (!button) throw new Error("missing auth locale selector");
  return button;
}

function authLocaleOptionDetails(): Array<{ locale: string; label: string; flagSrc: string }> {
  const list = getAuthLocaleSelect().closest(".locale-picker")?.querySelector(".locale-picker__list");
  return Array.from(list?.querySelectorAll('[role="option"]') ?? []).map((option) => ({
    locale: option.getAttribute("data-locale") ?? "",
    label: option.querySelector(".locale-picker__label")?.textContent ?? "",
    flagSrc: (option.querySelector(".locale-picker__flag") as HTMLImageElement | null)?.getAttribute("src") ?? "",
  }));
}

function authLocaleOptionValues(): string[] {
  return authLocaleOptionDetails().map((option) => option.locale);
}

const EXPECTED_LOCALE_FLAG_PATHS = [
  "/assets/flags/us.svg",
  "/assets/flags/cn.svg",
  "/assets/flags/in.svg",
  "/assets/flags/mx.svg",
  "/assets/flags/sa.svg",
  "/assets/flags/fr.svg",
  "/assets/flags/bd.svg",
  "/assets/flags/br.svg",
  "/assets/flags/id.svg",
  "/assets/flags/pk.svg",
  "/assets/flags/ru.svg",
  "/assets/flags/de.svg",
  "/assets/flags/jp.svg",
  "/assets/flags/tz.svg",
  "/assets/flags/vn.svg",
  "/assets/flags/tr.svg",
  "/assets/flags/kr.svg",
  "/assets/flags/ir.svg",
  "/assets/flags/th.svg",
  "/assets/flags/it.svg",
  "/assets/flags/my.svg",
  "/assets/flags/pl.svg",
  "/assets/flags/ua.svg",
];

const EXPECTED_PUBLIC_LOCALES = ["en", "zh", "hi", "es", "ar", "fr", "bn", "pt", "id", "ur", "ru", "de", "ja", "sw", "vi", "tr", "ko", "fa", "th", "it", "ms", "pl", "uk"];

async function selectAuthLocale(locale: string): Promise<void> {
  const button = getAuthLocaleSelect();
  button.click();
  const option = button.closest(".locale-picker")?.querySelector(`[role="option"][data-locale="${locale}"]`) as HTMLElement | null;
  if (!option) throw new Error(`missing auth locale option: ${locale}`);
  option.click();
  await flushPromises();
}

function getAuthLocalePickerSelectedLocale(): string {
  const selected = getAuthLocaleSelect()
    .closest(".locale-picker")
    ?.querySelector('[role="option"][aria-selected="true"]');
  return selected?.getAttribute("data-locale") ?? "";
}

describe("auth view i18n", () => {
  beforeEach(() => {
    vi.resetModules();
    document.body.innerHTML = "";
    localStorage.clear();
    apiFetchMock.mockReset();
    showToastMock.mockReset();
    redirectAfterAuthMock.mockReset();
    window.history.replaceState({}, "", "/");
  });

  afterEach(async () => {
    const i18n = await import("../i18n/index.js");
    i18n.resetI18nForTests();
    document.body.innerHTML = "";
    localStorage.clear();
    window.history.replaceState({}, "", "/");
    vi.restoreAllMocks();
  });

  it("renders English sign-in copy by default", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");

    auth.renderAuth({ next: "/dashboard", oidcEnabled: true, localAuthEnabled: true });

    expect(document.querySelector(".panel__title")?.textContent).toBe("Sign in");
    expect(document.querySelector(".muted")?.textContent?.trim()).toBe(
      "Authentication is enabled for this instance. Anonymous boards remain shareable by URL; durable projects require sign-in.",
    );
    expect(document.getElementById("authSsoBtn")?.textContent).toBe("Continue with SSO");
    expect(document.querySelector(".auth-divider span")?.textContent).toBe("or");
    expect((document.getElementById("authEmail") as HTMLInputElement | null)?.placeholder).toBe("Email");
    expect((document.getElementById("authPassword") as HTMLInputElement | null)?.placeholder).toBe("Password");
    expect(document.getElementById("loginBtn")?.textContent).toBe("Login");
    expect(document.getElementById("authPasswordToggle")?.getAttribute("aria-label")).toBe("Show password");
  });

  it("shows forgot password only for configured local sign-in outside bootstrap", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");

    auth.renderAuth({ localAuthEnabled: true, selfServicePasswordResetEnabled: true });
    expect(document.getElementById("authForgotPassword")?.textContent).toBe("Forgot password?");

    auth.renderAuth({ bootstrap: true, localAuthEnabled: true, selfServicePasswordResetEnabled: true });
    expect(document.getElementById("authForgotPassword")).toBeNull();

    auth.renderAuth({ oidcEnabled: true, localAuthEnabled: false, selfServicePasswordResetEnabled: true });
    expect(document.getElementById("authForgotPassword")).toBeNull();
    expect(document.getElementById("authForm")).toBeNull();

    auth.renderAuth({ localAuthEnabled: true, selfServicePasswordResetEnabled: false });
    expect(document.getElementById("authForgotPassword")).toBeNull();
  });

  it("opens the forgot step with the typed email and returns without losing sign-in state", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");

    auth.renderAuth({
      next: "/dashboard?tab=mine",
      oidcEnabled: true,
      localAuthEnabled: true,
      selfServicePasswordResetEnabled: true,
    });
    const emailEl = document.getElementById("authEmail") as HTMLInputElement;
    const passwordEl = document.getElementById("authPassword") as HTMLInputElement;
    emailEl.value = "first@example.com";
    passwordEl.value = "secret";
    document.getElementById("authPasswordToggle")?.click();
    document.getElementById("authForgotPassword")?.click();

    expect(document.querySelector(".panel__title")?.textContent).toBe("Reset your password");
    const forgotEmailEl = document.getElementById("authForgotEmail") as HTMLInputElement;
    expect(forgotEmailEl.value).toBe("first@example.com");
    expect(forgotEmailEl.getAttribute("aria-label")).toBe("Email");
    expect(document.activeElement).toBe(forgotEmailEl);

    forgotEmailEl.value = "edited@example.com";
    forgotEmailEl.dispatchEvent(new Event("input", { bubbles: true }));
    document.getElementById("authForgotBack")?.click();

    expect(document.querySelector(".panel__title")?.textContent).toBe("Sign in");
    expect((document.getElementById("authEmail") as HTMLInputElement).value).toBe("edited@example.com");
    expect((document.getElementById("authPassword") as HTMLInputElement).value).toBe("secret");
    expect((document.getElementById("authPassword") as HTMLInputElement).type).toBe("text");
    expect(document.getElementById("authSsoBtn")?.getAttribute("href")).toContain(
      "return_to=%2Fdashboard%3Ftab%3Dmine",
    );
    expect(document.getElementById("authForgotPassword")).not.toBeNull();
  });

  it("requests a reset with generic localized success, then restores sign-in and next", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");
    apiFetchMock
      .mockResolvedValueOnce({ message: "This address belongs to an account." })
      .mockResolvedValueOnce({});

    auth.renderAuth({
      next: "/projects?from=forgot",
      localAuthEnabled: true,
      selfServicePasswordResetEnabled: true,
    });
    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "password";
    document.getElementById("authForgotPassword")?.click();
    document.getElementById("authForgotForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(apiFetchMock).toHaveBeenNthCalledWith(1, "/api/auth/request-password-reset", {
      method: "POST",
      body: JSON.stringify({ email: "user@example.com" }),
    });
    expect(showToastMock).toHaveBeenCalledWith(
      "If an account exists for that email address, a password reset email has been sent.",
    );
    expect(document.querySelector(".panel__title")?.textContent).toBe("Sign in");
    expect((document.getElementById("authEmail") as HTMLInputElement).value).toBe("user@example.com");
    expect((document.getElementById("authPassword") as HTMLInputElement).value).toBe("password");
    expect(redirectAfterAuthMock).not.toHaveBeenCalled();

    document.getElementById("authForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(apiFetchMock).toHaveBeenNthCalledWith(2, "/api/auth/login", {
      method: "POST",
      body: JSON.stringify({ email: "user@example.com", password: "password" }),
    });
    expect(redirectAfterAuthMock).toHaveBeenCalledWith("/projects?from=forgot");
  });

  it("keeps the forgot step open and localizes rate-limit and fallback failures", async () => {
    await setupI18n("de");
    const auth = await import("./auth.js");
    apiFetchMock.mockRejectedValueOnce({ status: 429, message: "HTTP 429" });

    auth.renderAuth({ localAuthEnabled: true, selfServicePasswordResetEnabled: true });
    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    document.getElementById("authForgotPassword")?.click();
    document.getElementById("authForgotForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(showToastMock).toHaveBeenLastCalledWith("Zu viele Versuche. Bitte später erneut versuchen.");
    expect(document.querySelector(".panel__title")?.textContent).toBe("Passwort zurücksetzen");
    expect((document.getElementById("authForgotSubmit") as HTMLButtonElement).disabled).toBe(false);
    expect((document.getElementById("authForgotBack") as HTMLButtonElement).disabled).toBe(false);

    apiFetchMock.mockRejectedValueOnce({ status: 500, message: "HTTP 500" });
    document.getElementById("authForgotForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(showToastMock).toHaveBeenLastCalledWith(
      "Die Anfrage zum Zurücksetzen des Passworts konnte nicht gesendet werden.",
    );
    expect(document.querySelector(".panel__title")?.textContent).toBe("Passwort zurücksetzen");
  });

  it("disables forgot actions in flight and ignores duplicate submissions", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");
    let resolveRequest: ((value: unknown) => void) | undefined;
    apiFetchMock.mockImplementationOnce(() => new Promise((resolve) => {
      resolveRequest = resolve;
    }));

    auth.renderAuth({ localAuthEnabled: true, selfServicePasswordResetEnabled: true });
    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    document.getElementById("authForgotPassword")?.click();
    const form = document.getElementById("authForgotForm");
    form?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    form?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));

    expect(apiFetchMock).toHaveBeenCalledTimes(1);
    expect((document.getElementById("authForgotSubmit") as HTMLButtonElement).disabled).toBe(true);
    expect((document.getElementById("authForgotBack") as HTMLButtonElement).disabled).toBe(true);

    resolveRequest?.({});
    await flushPromises();
    expect(document.querySelector(".panel__title")?.textContent).toBe("Sign in");
  });

  it("ignores stale forgot completions after a newer auth render or navigation", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");
    let resolveRequest: ((value: unknown) => void) | undefined;
    let rejectRequest: ((reason?: unknown) => void) | undefined;
    apiFetchMock
      .mockImplementationOnce(() => new Promise((resolve) => {
        resolveRequest = resolve;
      }))
      .mockImplementationOnce(() => new Promise((_resolve, reject) => {
        rejectRequest = reject;
      }));

    auth.renderAuth({
      next: "/old-success",
      localAuthEnabled: true,
      selfServicePasswordResetEnabled: true,
    });
    (document.getElementById("authEmail") as HTMLInputElement).value = "old@example.com";
    document.getElementById("authForgotPassword")?.click();
    document.getElementById("authForgotForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));

    auth.renderAuth({
      next: "/newer-success",
      oidcEnabled: true,
      localAuthEnabled: true,
      selfServicePasswordResetEnabled: false,
    });
    resolveRequest?.({});
    await flushPromises();

    expect(showToastMock).not.toHaveBeenCalled();
    expect(document.querySelector(".panel__title")?.textContent).toBe("Sign in");
    expect((document.getElementById("authEmail") as HTMLInputElement).value).toBe("");
    expect(document.getElementById("authSsoBtn")?.getAttribute("href")).toContain("return_to=%2Fnewer-success");

    auth.renderAuth({
      next: "/old-error",
      localAuthEnabled: true,
      selfServicePasswordResetEnabled: true,
    });
    (document.getElementById("authEmail") as HTMLInputElement).value = "old@example.com";
    document.getElementById("authForgotPassword")?.click();
    document.getElementById("authForgotForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));

    document.body.innerHTML = `<div class="page page--projects"><h1>Newer page</h1></div>`;
    rejectRequest?.(new Error("network failed"));
    await flushPromises();

    expect(showToastMock).not.toHaveBeenCalled();
    expect(document.querySelector("h1")?.textContent).toBe("Newer page");
  });

  it("relocalizes an open forgot step without clearing the edited email", async () => {
    const i18n = await setupI18n("en");
    const auth = await import("./auth.js");

    auth.renderAuth({ localAuthEnabled: true, selfServicePasswordResetEnabled: true });
    (document.getElementById("authEmail") as HTMLInputElement).value = "first@example.com";
    document.getElementById("authForgotPassword")?.click();
    const forgotEmailEl = document.getElementById("authForgotEmail") as HTMLInputElement;
    forgotEmailEl.value = "edited@example.com";
    forgotEmailEl.dispatchEvent(new Event("input", { bubbles: true }));

    await i18n.setLocale("de");
    await flushPromises();

    expect(document.querySelector(".panel__title")?.textContent).toBe("Passwort zurücksetzen");
    expect(document.querySelector(".muted")?.textContent).toContain("30 Minuten");
    expect(document.getElementById("authForgotSubmit")?.textContent).toBe("Link zum Zurücksetzen senden");
    expect(document.getElementById("authForgotBack")?.textContent).toBe("Zurück zur Anmeldung");
    expect((document.getElementById("authForgotEmail") as HTMLInputElement).value).toBe("edited@example.com");
    expect((document.getElementById("authForgotEmail") as HTMLInputElement).getAttribute("aria-label")).toBe("E-Mail");
  });

  it("renders a public language selector in every auth shell", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");

    auth.renderAuth({ next: "/dashboard", oidcEnabled: true, localAuthEnabled: true });
    expect(authLocaleOptionValues()).toEqual(EXPECTED_PUBLIC_LOCALES);
    expect(authLocaleOptionDetails().map((option) => option.flagSrc)).toEqual(EXPECTED_LOCALE_FLAG_PATHS);
    expect(authLocaleOptionDetails().map((option) => option.label)).toEqual([
      "English",
      "简体中文",
      "हिन्दी",
      "Español (Latinoamérica)",
      "العربية",
      "Français",
      "বাংলা",
      "Português (Brasil)",
      "Bahasa Indonesia",
      "اردو",
      "Русский",
      "Deutsch",
      "日本語",
      "Kiswahili",
      "Tiếng Việt",
      "Türkçe",
      "한국어",
      "فارسی",
      "ไทย",
      "Italiano",
      "Bahasa Melayu",
      "Polski",
      "Українська",
    ]);
    expect(authLocaleOptionValues()).not.toContain("pseudo");
    expect(getAuthLocaleSelect().getAttribute("aria-label")).toBe("Language");

    auth.renderAuth({ next: "/projects", bootstrap: true, oidcEnabled: false, localAuthEnabled: true });
    expect(authLocaleOptionValues()).toEqual(EXPECTED_PUBLIC_LOCALES);

    auth.renderResetPassword("reset-token");
    expect(authLocaleOptionValues()).toEqual(EXPECTED_PUBLIC_LOCALES);

    apiFetchMock.mockResolvedValueOnce({
      requires2fa: true,
      tempToken: "temp-token",
      user: { id: 7, email: "user@example.com" },
    });
    auth.renderAuth({ next: "/dashboard", localAuthEnabled: true });
    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "password";
    document.getElementById("authForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(document.querySelector(".panel__title")?.textContent).toBe("Two-factor authentication");
    expect(authLocaleOptionValues()).toEqual(EXPECTED_PUBLIC_LOCALES);
    expect(authLocaleOptionValues()).not.toContain("pseudo");
  });

  it("pins the open auth locale list with fixed positioning", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");

    auth.renderAuth({ next: "/dashboard", oidcEnabled: true, localAuthEnabled: true });

    const button = getAuthLocaleSelect();
    button.click();

    const list = button.closest(".locale-picker")?.querySelector(".locale-picker__list") as HTMLUListElement;
    expect(list.hidden).toBe(false);
    expect(list.style.position).toBe("fixed");
    expect(list.style.top).toMatch(/px$/);
    expect(list.style.left || list.style.right).toMatch(/px$/);
    expect(list.style.minWidth).toMatch(/px$/);
    expect(list.style.zIndex).toBe("1000");
    expect(list.querySelectorAll('[role="option"]').length).toBe(EXPECTED_PUBLIC_LOCALES.length);
  });

  it("selecting Arabic in the auth selector sets RTL document direction", async () => {
    const i18n = await setupI18n("en");
    const auth = await import("./auth.js");

    auth.renderAuth({ next: "/dashboard", oidcEnabled: true, localAuthEnabled: true });
    await selectAuthLocale("ar");

    expect(i18n.getLocale()).toBe("ar");
    expect(document.documentElement.getAttribute("dir")).toBe("rtl");
    expect(document.documentElement.lang).toBe("ar");
    expect(document.querySelector(".panel__title")?.textContent).toBe("تسجيل الدخول");
  });

  it("selecting German in the auth selector persists locale and updates chrome without clearing sign-in fields", async () => {
    const i18n = await setupI18n("en");
    const auth = await import("./auth.js");

    auth.renderAuth({ next: "/dashboard?tab=mine", oidcEnabled: true, localAuthEnabled: true });
    const emailEl = document.getElementById("authEmail") as HTMLInputElement;
    const pwEl = document.getElementById("authPassword") as HTMLInputElement;
    emailEl.value = "user@example.com";
    pwEl.value = "secret";
    emailEl.dispatchEvent(new Event("input", { bubbles: true }));
    pwEl.dispatchEvent(new Event("input", { bubbles: true }));

    await selectAuthLocale("de");

    expect(i18n.getLocale()).toBe("de");
    expect(localStorage.getItem(i18n.LOCALE_STORAGE_KEY)).toBe("de");
    expect(document.cookie).toContain(`${i18n.LOCALE_STORAGE_KEY}=de`);
    expect(getAuthLocalePickerSelectedLocale()).toBe("de");
    expect(getAuthLocaleSelect().getAttribute("aria-label")).toBe("Sprache");
    expect(document.querySelector(".panel__title")?.textContent).toBe("Anmelden");
    expect(document.getElementById("authSsoBtn")?.textContent).toBe("Mit SSO fortfahren");
    expect((document.getElementById("authEmail") as HTMLInputElement).value).toBe("user@example.com");
    expect((document.getElementById("authPassword") as HTMLInputElement).value).toBe("secret");
  });

  it("selecting a locale in bootstrap preserves typed admin fields", async () => {
    const i18n = await setupI18n("en");
    const auth = await import("./auth.js");

    auth.renderAuth({ next: "/projects", bootstrap: true, localAuthEnabled: true });
    const nameEl = document.getElementById("authName") as HTMLInputElement;
    const emailEl = document.getElementById("authEmail") as HTMLInputElement;
    const pwEl = document.getElementById("authPassword") as HTMLInputElement;
    nameEl.value = "Admin";
    emailEl.value = "admin@example.com";
    pwEl.value = "bootstrap-secret";
    nameEl.dispatchEvent(new Event("input", { bubbles: true }));
    emailEl.dispatchEvent(new Event("input", { bubbles: true }));
    pwEl.dispatchEvent(new Event("input", { bubbles: true }));

    await selectAuthLocale("fr");
    await flushPromises();

    expect(i18n.getLocale()).toBe("fr");
    expect(document.querySelector(".panel__title")?.textContent).toBe("Configuration initiale");
    expect(getAuthLocalePickerSelectedLocale()).toBe("fr");
    expect((document.getElementById("authName") as HTMLInputElement).value).toBe("Admin");
    expect((document.getElementById("authEmail") as HTMLInputElement).value).toBe("admin@example.com");
    expect((document.getElementById("authPassword") as HTMLInputElement).value).toBe("bootstrap-secret");
  });

  it("keeps pseudo hidden from the auth selector while displaying a public fallback", async () => {
    const i18n = await setupI18n("pseudo");
    const auth = await import("./auth.js");

    auth.renderAuth({ next: "/dashboard", oidcEnabled: true, localAuthEnabled: true });

    expect(i18n.getLocale()).toBe("pseudo");
    expect(document.querySelector(".panel__title")?.textContent).toBe("[!! Sign in !!]");
    expect(authLocaleOptionValues()).toEqual(EXPECTED_PUBLIC_LOCALES);
    expect(authLocaleOptionDetails().map((option) => option.flagSrc)).toEqual(EXPECTED_LOCALE_FLAG_PATHS);
    expect(authLocaleOptionValues()).not.toContain("pseudo");
    expect(getAuthLocalePickerSelectedLocale()).toBe("en");
    expect(getAuthLocaleSelect().getAttribute("aria-label")).toBe("[!! Language !!]");
  });

  it("renders English bootstrap and reset-password copy", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");

    auth.renderAuth({ next: "/projects", bootstrap: true, oidcEnabled: false, localAuthEnabled: true });
    expect(document.querySelector(".panel__title")?.textContent).toBe("First-time setup");
    expect((document.getElementById("authName") as HTMLInputElement | null)?.placeholder).toBe("Name");
    expect(document.getElementById("bootstrapBtn")?.textContent).toBe("Bootstrap");

    auth.renderResetPassword("reset-token");
    expect(document.querySelector(".panel__title")?.textContent).toBe("Reset Password");
    expect(document.querySelector(".field__label")?.textContent).toBe("New password");
    expect(document.getElementById("resetPasswordSubmit")?.textContent).toBe("Reset Password");
  });

  it("renders catalog-backed sign-in strings for de, fr, pt, and pseudo", async () => {
    const cases: Array<[TestLocale, string, string, string]> = [
      ["de", "Anmelden", "Mit SSO fortfahren", "Passwort"],
      ["fr", "Se connecter", "Continuer avec le SSO", "Mot de passe"],
      ["pt", "Entrar", "Continuar com SSO", "Senha"],
      ["pseudo", "[!! Sign in !!]", "[!! Continue with SSO !!]", "[!! Password !!]"],
    ];

    for (const [locale, title, sso, passwordPlaceholder] of cases) {
      await setupI18n(locale);
      const auth = await import("./auth.js");
      auth.renderAuth({ next: "/dashboard", oidcEnabled: true, localAuthEnabled: true });

      expect(document.querySelector(".panel__title")?.textContent).toBe(title);
      expect(document.getElementById("authSsoBtn")?.textContent).toBe(sso);
      expect((document.getElementById("authPassword") as HTMLInputElement | null)?.placeholder).toBe(passwordPlaceholder);

      const i18n = await import("../i18n/index.js");
      i18n.resetI18nForTests();
      document.body.innerHTML = "";
      vi.resetModules();
    }
  });

  it("rerenders sign-in chrome on locale change without losing typed values or next", async () => {
    const i18n = await setupI18n("en");
    const auth = await import("./auth.js");

    auth.renderAuth({ next: "/dashboard?tab=mine", oidcEnabled: true, localAuthEnabled: true });

    const emailEl = document.getElementById("authEmail") as HTMLInputElement;
    const pwEl = document.getElementById("authPassword") as HTMLInputElement;
    const toggle = document.getElementById("authPasswordToggle") as HTMLButtonElement;
    emailEl.value = "user@example.com";
    pwEl.value = "secret";
    emailEl.dispatchEvent(new Event("input", { bubbles: true }));
    pwEl.dispatchEvent(new Event("input", { bubbles: true }));
    toggle.click();

    await i18n.setLocale("pseudo");
    await flushPromises();

    expect(document.querySelector(".panel__title")?.textContent).toBe("[!! Sign in !!]");
    expect(document.getElementById("authSsoBtn")?.textContent).toBe("[!! Continue with SSO !!]");
    expect(document.querySelector(".auth-divider span")?.textContent).toBe("[!! or !!]");
    expect((document.getElementById("authEmail") as HTMLInputElement).value).toBe("user@example.com");
    expect((document.getElementById("authPassword") as HTMLInputElement).value).toBe("secret");
    expect((document.getElementById("authPassword") as HTMLInputElement).type).toBe("text");
    expect(document.getElementById("authPasswordToggle")?.getAttribute("aria-label")).toBe("[!! Hide password !!]");
    expect(document.getElementById("authSsoBtn")?.getAttribute("href")).toContain("return_to=%2Fdashboard%3Ftab%3Dmine");
  });

  it("rerenders bootstrap chrome on locale change without losing typed values", async () => {
    const i18n = await setupI18n("en");
    const auth = await import("./auth.js");

    auth.renderAuth({ next: "/projects", bootstrap: true, localAuthEnabled: true });

    const nameEl = document.getElementById("authName") as HTMLInputElement;
    const emailEl = document.getElementById("authEmail") as HTMLInputElement;
    const pwEl = document.getElementById("authPassword") as HTMLInputElement;
    nameEl.value = "Admin";
    emailEl.value = "admin@example.com";
    pwEl.value = "bootstrap-secret";
    nameEl.dispatchEvent(new Event("input", { bubbles: true }));
    emailEl.dispatchEvent(new Event("input", { bubbles: true }));
    pwEl.dispatchEvent(new Event("input", { bubbles: true }));

    await i18n.setLocale("fr");
    await flushPromises();

    expect(document.querySelector(".panel__title")?.textContent).toBe("Configuration initiale");
    expect(document.getElementById("bootstrapBtn")?.textContent).toBe("Initialiser");
    expect((document.getElementById("authName") as HTMLInputElement).value).toBe("Admin");
    expect((document.getElementById("authEmail") as HTMLInputElement).value).toBe("admin@example.com");
    expect((document.getElementById("authPassword") as HTMLInputElement).value).toBe("bootstrap-secret");
  });

  it("rerenders 2FA chrome on locale change without losing the typed code and keeps next on success", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");
    apiFetchMock.mockResolvedValueOnce({
      requires2fa: true,
      tempToken: "temp-token",
      user: { id: 7, email: "user@example.com" },
    });
    apiFetchMock.mockResolvedValueOnce({});

    auth.renderAuth({ next: "/dashboard?tab=mine", localAuthEnabled: true });

    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "password";
    document.getElementById("authForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(document.querySelector(".panel__title")?.textContent).toBe("Two-factor authentication");
    const i18n = await import("../i18n/index.js");
    const codeEl = document.getElementById("auth2FACode") as HTMLInputElement;
    codeEl.value = "123456";
    codeEl.dispatchEvent(new Event("input", { bubbles: true }));

    await i18n.setLocale("pt");
    await flushPromises();

    expect(document.querySelector(".panel__title")?.textContent).toBe("Autenticação de dois fatores");
    expect((document.getElementById("auth2FACode") as HTMLInputElement).value).toBe("123456");
    expect((document.getElementById("auth2FACode") as HTMLInputElement).placeholder).toBe("Código para user@example.com");

    document.getElementById("auth2FAForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(apiFetchMock).toHaveBeenNthCalledWith(2, "/api/auth/login/2fa", {
      method: "POST",
      body: JSON.stringify({ tempToken: "temp-token", code: "123456" }),
    });
    expect(redirectAfterAuthMock).toHaveBeenCalledWith("/dashboard?tab=mine");
  });

  it("rerenders reset-password chrome on locale change without losing typed values or password visibility", async () => {
    const i18n = await setupI18n("en");
    const auth = await import("./auth.js");

    auth.renderResetPassword("reset-token");

    const newPwEl = document.getElementById("resetNewPassword") as HTMLInputElement;
    const confirmPwEl = document.getElementById("resetConfirmPassword") as HTMLInputElement;
    const toggle = document.getElementById("resetPasswordToggle") as HTMLButtonElement;
    newPwEl.value = "new-secret";
    confirmPwEl.value = "new-secret";
    newPwEl.dispatchEvent(new Event("input", { bubbles: true }));
    confirmPwEl.dispatchEvent(new Event("input", { bubbles: true }));
    toggle.click();

    await i18n.setLocale("de");
    await flushPromises();

    expect(document.querySelector(".panel__title")?.textContent).toBe("Passwort zurücksetzen");
    expect(document.querySelector(".field__label")?.textContent).toBe("Neues Passwort");
    expect((document.getElementById("resetNewPassword") as HTMLInputElement).value).toBe("new-secret");
    expect((document.getElementById("resetConfirmPassword") as HTMLInputElement).value).toBe("new-secret");
    expect((document.getElementById("resetNewPassword") as HTMLInputElement).type).toBe("text");
    expect(document.getElementById("resetPasswordToggle")?.getAttribute("aria-label")).toBe("Passwort verbergen");
  });

  it("keeps login submission behavior unchanged on success", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");
    apiFetchMock.mockResolvedValueOnce({});

    auth.renderAuth({ next: "/projects?from=auth", localAuthEnabled: true });
    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "password";
    document.getElementById("authForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(apiFetchMock).toHaveBeenCalledWith("/api/auth/login", {
      method: "POST",
      body: JSON.stringify({ email: "user@example.com", password: "password" }),
    });
    expect(redirectAfterAuthMock).toHaveBeenCalledWith("/projects?from=auth");
  });

  it("keeps bootstrap submission behavior unchanged on success", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");
    apiFetchMock.mockResolvedValueOnce({});

    auth.renderAuth({ next: "/projects", bootstrap: true, localAuthEnabled: true });
    (document.getElementById("authName") as HTMLInputElement).value = "Admin";
    (document.getElementById("authEmail") as HTMLInputElement).value = "admin@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "secret";
    document.getElementById("bootstrapBtn")?.dispatchEvent(new Event("click", { bubbles: true }));
    await flushPromises();

    expect(apiFetchMock).toHaveBeenCalledWith("/api/auth/bootstrap", {
      method: "POST",
      body: JSON.stringify({ name: "Admin", email: "admin@example.com", password: "secret" }),
    });
    expect(redirectAfterAuthMock).toHaveBeenCalledWith("/projects");
  });

  it("keeps reset-password submission behavior unchanged on success", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");
    apiFetchMock.mockResolvedValueOnce({});
    window.history.replaceState({}, "", "/auth/reset-password?token=reset-token");

    auth.renderResetPassword("reset-token");
    (document.getElementById("resetNewPassword") as HTMLInputElement).value = "new-password";
    (document.getElementById("resetConfirmPassword") as HTMLInputElement).value = "new-password";
    document.getElementById("resetPasswordForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(apiFetchMock).toHaveBeenCalledWith("/api/auth/reset-password", {
      method: "POST",
      body: JSON.stringify({ token: "reset-token", new_password: "new-password" }),
    });
    expect(showToastMock).toHaveBeenCalledWith("Password reset successfully. Please log in.");
    expect(window.location.pathname).toBe("/");
  });

  it("localizes compatible auth API failures and preserves raw backend detail when no mapping exists", async () => {
    const i18n = await setupI18n("de");
    const auth = await import("./auth.js");

    apiFetchMock.mockRejectedValueOnce({
      status: 401,
      message: "raw unauthorized",
      data: { error: { code: "UNAUTHORIZED", message: "raw unauthorized" } },
    });

    auth.renderAuth({ next: "/dashboard", localAuthEnabled: true });
    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "bad";
    document.getElementById("authForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(showToastMock).toHaveBeenLastCalledWith("Nicht angemeldet");

    apiFetchMock.mockRejectedValueOnce({
      status: 400,
      message: "Password must be at least 12 characters",
      data: { error: { message: "Password must be at least 12 characters" } },
    });

    auth.renderAuth({ next: "/projects", bootstrap: true, localAuthEnabled: true });
    (document.getElementById("authName") as HTMLInputElement).value = "Admin";
    (document.getElementById("authEmail") as HTMLInputElement).value = "admin@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "short";
    document.getElementById("bootstrapBtn")?.dispatchEvent(new Event("click", { bubbles: true }));
    await flushPromises();

    expect(showToastMock).toHaveBeenLastCalledWith("Password must be at least 12 characters");

    await i18n.setLocale("en");
  });

  it("shows localized OIDC query-param failures, cleans the URL, and does not retrigger them on locale change", async () => {
    const i18n = await setupI18n("fr");
    const auth = await import("./auth.js");
    window.history.replaceState({}, "", "/dashboard?oidc_error=state_invalid");

    auth.renderAuth({ next: "/dashboard", oidcEnabled: true, localAuthEnabled: true });

    expect(showToastMock).toHaveBeenCalledWith("La session de connexion a expiré ou est invalide. Veuillez réessayer.");
    expect(window.location.search).toBe("");

    showToastMock.mockClear();
    await i18n.setLocale("pt");
    await flushPromises();

    expect(showToastMock).not.toHaveBeenCalled();
  });

  it("preserves explicit next exactly and strips oidc_error from derived SSO return_to", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");
    window.history.replaceState({}, "", "/dashboard?tab=mine&oidc_error=state_invalid");

    auth.renderAuth({ next: "/dashboard?tab=mine", oidcEnabled: true, localAuthEnabled: true });

    expect(showToastMock).toHaveBeenCalledWith("Login session expired or invalid. Please try again.");
    expect(window.location.search).toBe("?tab=mine");
    expect(document.getElementById("authSsoBtn")?.getAttribute("href")).toContain(
      "return_to=%2Fdashboard%3Ftab%3Dmine",
    );
    expect(document.getElementById("authSsoBtn")?.getAttribute("href")).not.toContain("oidc_error");

    showToastMock.mockClear();
    document.body.innerHTML = "";
    vi.resetModules();
    await setupI18n("en");
    const authDerived = await import("./auth.js");
    window.history.replaceState({}, "", "/projects?oidc_error=provider");

    authDerived.renderAuth({ oidcEnabled: true, localAuthEnabled: true });

    expect(showToastMock).toHaveBeenCalledWith("The identity provider returned an error.");
    expect(window.location.search).toBe("");
    expect(document.getElementById("authSsoBtn")?.getAttribute("href")).toContain("return_to=%2Fprojects");
    expect(document.getElementById("authSsoBtn")?.getAttribute("href")).not.toContain("oidc_error");
  });

  it("strips oidc_error from explicit router-style next for SSO return_to after cleanup", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");
    window.history.replaceState({}, "", "/dashboard?oidc_error=token");

    auth.renderAuth({
      next: "/dashboard?oidc_error=token",
      oidcEnabled: true,
      localAuthEnabled: true,
    });

    expect(showToastMock).toHaveBeenCalledOnce();
    expect(window.location.search).toBe("");
    expect(document.getElementById("authSsoBtn")?.getAttribute("href")).toContain("return_to=%2Fdashboard");
    expect(document.getElementById("authSsoBtn")?.getAttribute("href")).not.toContain("oidc_error");
  });

  it("uses the sanitized explicit next for bootstrap, login, and 2FA redirects after OIDC cleanup", async () => {
    await setupI18n("en");
    const auth = await import("./auth.js");
    const sanitizedNext = "/projects?tab=mine";
    const explicitNext = `${sanitizedNext}&oidc_error=state_invalid`;

    window.history.replaceState({}, "", "/auth?oidc_error=state_invalid");
    apiFetchMock.mockResolvedValueOnce({});
    auth.renderAuth({ next: explicitNext, bootstrap: true, oidcEnabled: true, localAuthEnabled: true });
    (document.getElementById("authName") as HTMLInputElement).value = "Admin";
    (document.getElementById("authEmail") as HTMLInputElement).value = "admin@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "secret";
    document.getElementById("bootstrapBtn")?.dispatchEvent(new Event("click", { bubbles: true }));
    await flushPromises();
    expect(redirectAfterAuthMock).toHaveBeenLastCalledWith(sanitizedNext);

    document.body.innerHTML = "";
    redirectAfterAuthMock.mockReset();
    apiFetchMock.mockReset();
    window.history.replaceState({}, "", "/auth?oidc_error=state_invalid");
    apiFetchMock.mockResolvedValueOnce({});
    auth.renderAuth({ next: explicitNext, oidcEnabled: true, localAuthEnabled: true });
    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "password";
    document.getElementById("authForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();
    expect(redirectAfterAuthMock).toHaveBeenLastCalledWith(sanitizedNext);

    document.body.innerHTML = "";
    redirectAfterAuthMock.mockReset();
    apiFetchMock.mockReset();
    window.history.replaceState({}, "", "/auth?oidc_error=state_invalid");
    apiFetchMock.mockResolvedValueOnce({
      requires2fa: true,
      tempToken: "temp-token",
      user: { id: 7, email: "user@example.com" },
    });
    apiFetchMock.mockResolvedValueOnce({});
    auth.renderAuth({ next: explicitNext, oidcEnabled: true, localAuthEnabled: true });
    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "password";
    document.getElementById("authForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();
    (document.getElementById("auth2FACode") as HTMLInputElement).value = "123456";
    document.getElementById("auth2FAForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();
    expect(redirectAfterAuthMock).toHaveBeenLastCalledWith(sanitizedNext);
  });

  it("binds one auth locale listener across repeated auth renders and transitions", async () => {
    const i18n = await import("../i18n/index.js");
    await i18n.initI18n({ locale: "en", loadLocale: loader() });
    const addListenerSpy = vi.spyOn(document, "addEventListener");
    const auth = await import("./auth.js");
    apiFetchMock.mockResolvedValueOnce({
      requires2fa: true,
      tempToken: "temp-token",
      user: { id: 7, email: "user@example.com" },
    });

    auth.renderAuth({ next: "/dashboard", oidcEnabled: true, localAuthEnabled: true });
    auth.renderAuth({
      next: "/projects",
      oidcEnabled: true,
      localAuthEnabled: true,
      selfServicePasswordResetEnabled: true,
    });
    document.getElementById("authForgotPassword")?.click();
    document.getElementById("authForgotBack")?.click();

    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "password";
    document.getElementById("authForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    auth.renderResetPassword("reset-token");

    const localeListenerAdds = addListenerSpy.mock.calls.filter(
      ([eventName]) => eventName === i18n.I18N_LOCALE_CHANGED,
    );
    expect(localeListenerAdds).toHaveLength(1);
    addListenerSpy.mockRestore();
  });

  it("does not apply auth translations after navigating away from the auth page", async () => {
    const i18n = await setupI18n("en");
    const auth = await import("./auth.js");

    auth.renderAuth({ next: "/dashboard", localAuthEnabled: true });
    document.body.innerHTML = `<div class="page page--projects"><h1 class="panel__title">Projects</h1></div>`;

    await i18n.setLocale("de");
    await flushPromises();

    expect(document.querySelector(".panel__title")?.textContent).toBe("Projects");
  });

  it("keeps login submission payload unchanged after locale change", async () => {
    const i18n = await setupI18n("en");
    const auth = await import("./auth.js");
    apiFetchMock.mockResolvedValueOnce({});

    auth.renderAuth({ next: "/projects?from=auth", localAuthEnabled: true });
    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "password";
    (document.getElementById("authEmail") as HTMLInputElement).dispatchEvent(new Event("input", { bubbles: true }));
    (document.getElementById("authPassword") as HTMLInputElement).dispatchEvent(new Event("input", { bubbles: true }));

    await i18n.setLocale("fr");
    await flushPromises();

    document.getElementById("authForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(apiFetchMock).toHaveBeenCalledWith("/api/auth/login", {
      method: "POST",
      body: JSON.stringify({ email: "user@example.com", password: "password" }),
    });
    expect(redirectAfterAuthMock).toHaveBeenCalledWith("/projects?from=auth");
  });

  it("localizes 429 auth failures through errors.RATE_LIMITED and uses fallback keys only without useful detail", async () => {
    await setupI18n("de");
    const auth = await import("./auth.js");

    apiFetchMock.mockRejectedValueOnce({ status: 429, message: "HTTP 429" });
    auth.renderAuth({ next: "/dashboard", localAuthEnabled: true });
    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "password";
    document.getElementById("authForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();
    expect(showToastMock).toHaveBeenLastCalledWith("Zu viele Versuche. Bitte später erneut versuchen.");

    apiFetchMock.mockRejectedValueOnce({ status: 500, message: "HTTP 500" });
    auth.renderAuth({ next: "/dashboard", localAuthEnabled: true });
    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "password";
    document.getElementById("authForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();
    expect(showToastMock).toHaveBeenLastCalledWith("Anmeldung fehlgeschlagen.");
  });

  it("shows the localized reset-password mismatch message", async () => {
    await setupI18n("pt");
    const auth = await import("./auth.js");

    auth.renderResetPassword("reset-token");
    (document.getElementById("resetNewPassword") as HTMLInputElement).value = "one";
    (document.getElementById("resetConfirmPassword") as HTMLInputElement).value = "two";
    document.getElementById("resetPasswordForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));

    expect(showToastMock).toHaveBeenCalledWith("As senhas não coincidem");
    expect(apiFetchMock).not.toHaveBeenCalled();
  });
});
