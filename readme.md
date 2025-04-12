untuk memulai project golang

go mod init namafolder-kamu

untuk install package go get -u github.com/yourpackage


fixing bug
penambahan koma , pada detail atau keterangan budget terjadi error krn belum di handling dari input user
penambahan fitur total pengeluaran hari ini, berdasarkan hari aja. dengan mengetik /budget hari ini, /budget-kemarin, /budget-tanggal-14, /budget-bulan-2022

Ada apa tidak tentang cara membuat credentials.json yang lebih memudahkan pengguna agar tidak ribet.? TIDAK ADA JADI HARUS BUAT CREDENTIALS

edit persiapan deploy di docker 12 april
________________________________________________________________________________
hipotesa ku, membuat tombol konfirmasi di chatbotnya, untuk login ke OAuth 2.0 sebagai kunci untuk mendapatkan kredentials. ? apakah bisa? jadi pertama kali saat bot di klik start, membuat setup akun dg kredential yang diperlukan.
Jadi aku harus membuat, start button pertama kali. oauth diperlukan untuk masuk ke akun dan create spreadsheet dg akses write.
________________________________________________________________________________

Beberapa cara untuk mempermudah pengguna dalam menyediakan informasi untuk credentials.json:
1. Instruksi yang Jelas dan DetailDokumentasi Langkah-demi-Langkah: 
Berikan panduan tertulis yang sangat jelas dan detail tentang cara mendapatkan kredensial Google Cloud. Sertakan tangkapan layar jika memungkinkan.
Video Tutorial: Buat video tutorial singkat yang menunjukkan proses pembuatan kredensial secara visual. Ini akan sangat membantu pengguna yang kurang familiar dengan Google Cloud Console.
Contoh File credentials.json: Berikan contoh template file credentials.json dengan placeholder yang jelas. Pengguna hanya perlu mengganti nilai placeholder dengan informasi yang sesuai.

2. Alat Bantu atau Skrip Konfigurasi Skrip Interaktif: 
Buat skrip (misalnya, dalam Go atau bahasa skrip lainnya) yang memandu pengguna melalui proses pembuatan kredensial. Skrip dapat menanyakan informasi yang diperlukan dan membuat file credentials.json secara otomatis.Aplikasi Konfigurasi: Jika memungkinkan, buat aplikasi kecil yang menyediakan antarmuka pengguna grafis (GUI) untuk mengumpulkan informasi kredensial. Aplikasi ini dapat menyederhanakan proses bagi pengguna non-teknis.

3. Penyederhanaan Proses Kredensial (Jika Memungkinkan)Opsi Otentikasi Alternatif: 
Jika memungkinkan, pertimbangkan opsi otentikasi alternatif yang lebih mudah bagi pengguna, seperti menggunakan kunci API (jika Google Sheets API mendukungnya dengan sesuai). Namun, selalu prioritaskan keamanan.
Delegasi Proyek Google Cloud: Jika Anda memiliki kontrol atas proyek Google Cloud yang digunakan, Anda dapat melakukan pra-konfigurasi tertentu untuk mengurangi langkah-langkah yang diperlukan pengguna.

4. Validasi dan Umpan Balik yang BaikValidasi Kredensial: Dalam kode Anda, tambahkan validasi untuk memastikan bahwa file credentials.json yang diberikan valid dan berisi informasi yang diperlukan.
Pesan Kesalahan yang Jelas: Jika terjadi kesalahan terkait kredensial (misalnya, file tidak valid, informasi tidak lengkap), berikan pesan kesalahan yang jelas dan informatif yang menunjukkan cara memperbaikinya.Contoh Pesan Kesalahan yang Lebih Baik (Go):   if err != nil {
       log.Fatalf("Error: Invalid credentials file. Please ensure the file is a valid JSON file obtained from the Google Cloud Console and contains the necessary information (client_id, client_secret, etc.).  See the documentation for detailed instructions: [Link ke Dokumentasi Anda]", err)
   }
Dengan menerapkan pendekatan-pendekatan ini, Anda dapat mengurangi kesulitan pengguna dalam menyediakan kredensial yang diperlukan dan meningkatkan pengalaman pengguna secara keseluruhan.
________________________________________________________________________________
________________________________________________________________________________


penambahan fitur agenda harian dan mingguan dulu aja. agenda, jam berapa, dan apa yang harus dilakukan.

penambahan tanggal dalam inputan?

penambahan fitur pada tanggal sekian pengeluaran apa saja /14-juni


------------------------------------------------------------------------
terakhir aja setelah banyak fitur gitu, lalu dibungkus dengan bawah ini
penambahan pusat command list contoh /menu
di dalam menu ada banyak menu

Simpan waktu otomatis

Lihat laporan mingguan

Tambah tombol keyboard, dsb