import { BrowserRouter, Route, Routes } from "react-router-dom";

import { SessionExpiredHost } from "@/components/SessionExpiredHost";
import { Toaster } from "@/components/ui/sonner";
import HomePage from "@/pages/HomePage";
import LoginPage from "@/pages/LoginPage";
import NotFoundPage from "@/pages/NotFoundPage";

function App() {
  return (
    <>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/" element={<HomePage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Routes>
      </BrowserRouter>
      <Toaster />
      <SessionExpiredHost />
    </>
  );
}

export default App;
